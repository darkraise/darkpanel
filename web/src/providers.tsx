import { useLayoutEffect, type ReactNode } from "react"
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query"
import { ThemeProvider, useTheme } from "darkraise-ui/theme"
import { RouterAdapterProvider } from "darkraise-ui/router"
import { routerAdapter } from "@/router/adapter"
import { themeConfig } from "@/theme.config"

// ThemeProvider sets no accent or surface attribute, and globals.css scopes
// its contrast overrides to the default accent and surface.
function ThemeAttributes() {
  const { accentColor, surfaceColor } = useTheme()
  useLayoutEffect(() => {
    document.documentElement.dataset.accent = accentColor
    document.documentElement.dataset.surface = surfaceColor
  }, [accentColor, surfaceColor])
  return null
}

// Providers wraps the app below its router: the router differs between the
// browser (BrowserRouter) and the tests (MemoryRouter).
export function Providers({ client, children }: { client: QueryClient; children: ReactNode }) {
  return (
    <ThemeProvider config={themeConfig}>
      <ThemeAttributes />
      <QueryClientProvider client={client}>
        <RouterAdapterProvider value={routerAdapter}>{children}</RouterAdapterProvider>
      </QueryClientProvider>
    </ThemeProvider>
  )
}
