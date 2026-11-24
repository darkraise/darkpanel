// focusFirstInvalid moves focus, once the errors have rendered, to the first
// field a failed submit marked aria-invalid; its error note is tied to it by
// aria-describedby, so a screen reader reads the problem with the field.
export function focusFirstInvalid(form: HTMLFormElement) {
  window.setTimeout(() => form.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus())
}
