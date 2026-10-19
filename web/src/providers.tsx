import type { ReactNode } from "react"
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query"
import { ThemeProvider } from "darkraise-ui/theme"
import { RouterAdapterProvider } from "darkraise-ui/router"
import { routerAdapter } from "@/router/adapter"
import { themeConfig } from "@/theme.config"

// Providers wraps the app below its router: the router differs between the
// browser (BrowserRouter) and the tests (MemoryRouter).
export function Providers({ client, children }: { client: QueryClient; children: ReactNode }) {
  return (
    <ThemeProvider config={themeConfig}>
      <QueryClientProvider client={client}>
        <RouterAdapterProvider value={routerAdapter}>{children}</RouterAdapterProvider>
      </QueryClientProvider>
    </ThemeProvider>
  )
}
