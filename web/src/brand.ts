import { useBrandStore } from "darkraise-ui/layout"

export const APP_NAME = "Darkpanel"

// The sidebar header reads its name from darkraise-ui's brand store, which
// defaults to "App".
export function applyBrand(): void {
  useBrandStore.getState().setAppName(APP_NAME)
}
