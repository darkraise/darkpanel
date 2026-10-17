import type { ReactNode } from "react"
import { ThemeProvider } from "darkraise-ui/theme"
import { SidebarLayout } from "darkraise-ui/layout"
import {
  RouterAdapterProvider,
  useOptionalRouterAdapter,
} from "darkraise-ui/router"
import { themeConfig } from "./theme.config"

// SidebarLayout requires a router adapter. Until the app has a router, provide
// the kit's fallback adapter, which useOptionalRouterAdapter returns outside a
// provider and which navigates with window.location.
function FallbackRouter({ children }: { children: ReactNode }) {
  const adapter = useOptionalRouterAdapter()
  return <RouterAdapterProvider value={adapter}>{children}</RouterAdapterProvider>
}

export function App() {
  return (
    <ThemeProvider config={themeConfig}>
      <FallbackRouter>
        <SidebarLayout nav={[]} showThemeSwitcher={true}>
          <div className="flex flex-col items-center justify-center gap-4 py-16">
            <h1 className="text-4xl font-medium">Darkpanel</h1>
            <p className="text-muted-foreground">
              The walking skeleton replaces this page.
            </p>
          </div>
        </SidebarLayout>
      </FallbackRouter>
    </ThemeProvider>
  )
}
