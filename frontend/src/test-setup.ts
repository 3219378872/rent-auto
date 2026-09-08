import '@testing-library/jest-dom/vitest'

// jsdom does not implement native dialog visibility/focus management.
Object.defineProperties(HTMLDialogElement.prototype, {
  showModal: { configurable: true, value(this: HTMLDialogElement) { this.open = true } },
  close: { configurable: true, value(this: HTMLDialogElement) { this.open = false } },
})
