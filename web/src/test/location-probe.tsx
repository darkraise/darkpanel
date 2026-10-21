import { useState } from "react"
import { useLocation } from "react-router"

// The probe also lists every location it has seen, so a test can tell a
// direct navigation from one that passed through another address.
export function LocationProbe() {
  const location = useLocation()
  const current = location.pathname + location.search
  const [seen, setSeen] = useState([current])
  if (seen[seen.length - 1] !== current) setSeen([...seen, current])
  return (
    <>
      <output data-testid="location">{current}</output>
      <output data-testid="locations">{seen.join(" ")}</output>
    </>
  )
}
