import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "../../shared/style.css";
import { App } from "./App";

createRoot(document.getElementById("app")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
