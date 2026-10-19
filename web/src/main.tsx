import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter } from "react-router"
import { QueryClient } from "@tanstack/react-query"
import "@fontsource-variable/hanken-grotesk"
import "@fontsource-variable/jetbrains-mono"
import { ApiError } from "@/api/client"
import { App } from "@/app"
import { Providers } from "@/providers"
import "./styles/globals.css"

// A refused request (4xx) fails the same way when retried, so only network
// and server failures are retried.
const client = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (failures, error) => failures < 2 && !(error instanceof ApiError && error.status >= 400 && error.status < 500),
      refetchOnWindowFocus: false,
    },
  },
})

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <Providers client={client}>
        <App />
      </Providers>
    </BrowserRouter>
  </StrictMode>,
)
