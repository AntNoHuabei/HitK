import React from "react";
import ReactDOM from "react-dom/client";
import ThemeProvider from "./theme/ThemeContext";
import App from "./App";
import "./styles/base.css";
import "./styles/app-shell.css";
import "./styles/chat.css";
import "./styles/citations.css";
import "./styles/permissions.css";
import "./styles/settings.css";
import "./styles/knowledge.css";

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <ThemeProvider>
      <App />
    </ThemeProvider>
  </React.StrictMode>,
);
