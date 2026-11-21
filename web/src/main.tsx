// SPDX-License-Identifier: AGPL-3.0-or-later

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
// Redesign (ADR-0037): the theme is set on <html data-theme> in index.html.
import "./ui/tokens.css";
import "./ui/base.css";
import "./ui/ui.css";
import "./ui/shell.css";

const root = document.getElementById("root");
if (!root) throw new Error("missing #root");

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
