package integration

// Sending what was queued (blueprint H6).
//
// # Nothing is sent inline
//
// A webhook is queued inside the transaction of whatever happened and sent
// later, by this. An HTTP call on a sale's commit path would hold a database
// connection open for as long as somebody else's server takes to answer, and a
// receiver having a bad afternoon would become a shop whose tills are slow.
//
// # A receiver that is down is not punished for it
//
// Retries back off — a minute, then five, then twenty-five, then two hours —
// and stop after six attempts. Retrying forever fills a queue nobody drains;
// giving up on the first failure loses a sale record because somebody's server
// restarted. Six attempts spans about three hours, which is long enough to
// cover a deploy and short enough that a genuinely dead endpoint is visible on
// the screen the same day.

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

// maxAttempts is where a delivery is abandoned.
const maxAttempts = 6

// backoff is how long to wait before attempt n+1.
//
// A table rather than a formula, because the shape matters more than the
// arithmetic and a reader should be able to see the whole schedule at once.
var backoff = []time.Duration{
	time.Minute,
	5 * time.Minute,
	25 * time.Minute,
	time.Hour,
	2 * time.Hour,
	2 * time.Hour,
}

// Dispatch sends what is due for one tenant, and returns how many it sent.
//
// Each delivery is claimed, sent and recorded on its own transaction. One
// transaction around the batch would hold every row locked for the length of
// the slowest receiver, and a crash halfway would replay deliveries that had
// already arrived — a shop's accounting system being told about the same sale
// twice is worse than being told late.
func (s *Service) Dispatch(
	ctx context.Context, tenantID uuid.UUID, client *http.Client, limit int,
) (int, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if s.cipher == nil {
		// Without the keyring the signing secret cannot be opened, and an
		// unsigned delivery is one a receiver has no way to trust. Saying so is
		// better than sending something they will reject.
		return 0, nil
	}

	sent := 0
	for i := 0; i < limit; i++ {
		done, err := s.dispatchOne(ctx, tenantID, client)
		if err != nil {
			return sent, err
		}
		if !done {
			break
		}
		sent++
	}
	return sent, nil
}

// dispatchOne claims a single delivery and sees it through. It returns false
// when there was nothing due.
func (s *Service) dispatchOne(
	ctx context.Context, tenantID uuid.UUID, client *http.Client,
) (bool, error) {
	var (
		deliveryID uuid.UUID
		url        string
		event      string
		payload    []byte
		sealed     []byte
		attempts   int
	)

	// Claimed with SKIP LOCKED so two workers never send the same delivery.
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT d.id, w.url, d.event, d.payload::text, w.secret_enc, d.attempts
			FROM webhook_delivery d
			JOIN webhook_endpoint w ON w.id = d.endpoint_id
			WHERE d.status = 'queued' AND w.is_active
			  AND (d.next_attempt_at IS NULL OR d.next_attempt_at <= now())
			ORDER BY d.created_at
			FOR UPDATE OF d SKIP LOCKED
			LIMIT 1`).
			Scan(&deliveryID, &url, &event, &payload, &sealed, &attempts)
	})
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, db.Translate(err, "")
	}

	secretHex, err := s.cipher.Open(sealed)
	if err != nil {
		return true, s.recordFailure(ctx, tenantID, deliveryID, attempts, 0,
			"the signing secret for this endpoint could not be opened")
	}
	secret, err := hex.DecodeString(string(secretHex))
	if err != nil {
		return true, s.recordFailure(ctx, tenantID, deliveryID, attempts, 0,
			"the signing secret for this endpoint is not readable")
	}

	status, sendErr := send(ctx, client, url, event, deliveryID, secret, payload)
	if sendErr != nil {
		return true, s.recordFailure(ctx, tenantID, deliveryID, attempts, status,
			sendErr.Error())
	}

	err = s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE webhook_delivery
			SET status = 'delivered', attempts = attempts + 1,
			    response_status = $2, delivered_at = now(),
			    next_attempt_at = NULL, last_error = NULL
			WHERE id = $1`, deliveryID, status)
		return e
	})
	return true, db.Translate(err, "")
}

// send posts one delivery and reports the receiver's answer.
//
// Any 2xx is success. A receiver that answers 202 has taken responsibility for
// the event, and insisting on 200 would fail every queue-backed integration.
func send(
	ctx context.Context, client *http.Client, url, event string,
	deliveryID uuid.UUID, secret, body []byte,
) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
		bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "RawSyst-Webhook/1")
	req.Header.Set("X-RawSyst-Event", event)
	// The delivery id, so a receiver can recognise a retry of something it has
	// already processed. At-least-once is what this queue promises, and a
	// receiver has no way to be idempotent without an id to be idempotent on.
	req.Header.Set("X-RawSyst-Delivery", deliveryID.String())
	req.Header.Set("X-RawSyst-Signature", "sha256="+Sign(secret, body))

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// Drained and discarded, capped. An undrained body leaks the connection out
	// of the pool; an unbounded read lets a hostile receiver return a gigabyte.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, fmt.Errorf("the endpoint answered %d", resp.StatusCode)
}

func (s *Service) recordFailure(
	ctx context.Context, tenantID, deliveryID uuid.UUID,
	attempts, status int, reason string,
) error {
	next := attempts
	if next >= len(backoff) {
		next = len(backoff) - 1
	}
	retryAt := time.Now().Add(backoff[next])

	// Abandoned rather than retried forever. The row stays, with the reason on
	// it, because a shop asking why its integration missed a week needs the
	// failures to still be there.
	final := attempts+1 >= maxAttempts

	return db.Translate(s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE webhook_delivery
			SET attempts = attempts + 1,
			    status = CASE WHEN $4 THEN 'abandoned' ELSE 'queued' END,
			    next_attempt_at = CASE WHEN $4 THEN NULL ELSE $3 END,
			    response_status = nullif($5, 0),
			    last_error = $2
			WHERE id = $1`, deliveryID, reason, retryAt, final, status)
		return e
	}), "")
}
