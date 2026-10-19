import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import "@fontsource-variable/hanken-grotesk"
import "@fontsource-variable/jetbrains-mono"
import { App } from "./app"
import "./styles/globals.css"

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
