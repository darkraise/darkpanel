import { useLocation, useNavigate } from "react-router"
import { useQueryClient } from "@tanstack/react-query"
import type { RouterAdapter } from "darkraise-ui/router"
import { AdapterLink } from "./adapter-link"

function useAdapterNavigate(): (to: string) => void {
  const navigate = useNavigate()
  return (to) => {
    void navigate(to)
  }
}

function useAdapterPathname(): string {
  return useLocation().pathname
}

function useAdapterBack(): () => void {
  const navigate = useNavigate()
  return () => {
    void navigate(-1)
  }
}

function useAdapterInvalidate(): () => void {
  const client = useQueryClient()
  return () => {
    void client.invalidateQueries()
  }
}

export const routerAdapter: RouterAdapter = {
  Link: AdapterLink,
  useNavigate: useAdapterNavigate,
  usePathname: useAdapterPathname,
  useBack: useAdapterBack,
  useInvalidate: useAdapterInvalidate,
}
