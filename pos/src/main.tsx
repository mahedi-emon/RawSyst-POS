// Entry point.
//
// The API base comes from the environment because a terminal in a shop points
// at that tenant's region, and a developer's points at localhost. Hard-coding
// it would mean rebuilding the binary per deployment.

import React from 'react';
import { createRoot } from 'react-dom/client';

import { App } from './App';
import { AuthProvider } from './auth/session';
// The design system first: it defines the tokens everything else consumes.
import './design-system.css';
import './styles.css';
import './dashboard/dashboard.css';

const baseUrl = import.meta.env.VITE_API_BASE_URL ?? 'http://127.0.0.1:8080';

const root = document.getElementById('root');
if (!root) throw new Error('the application root is missing from the page');

createRoot(root).render(
  <React.StrictMode>
    <AuthProvider baseUrl={baseUrl}>
      <App />
    </AuthProvider>
  </React.StrictMode>,
);
