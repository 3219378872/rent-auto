import '@testing-library/jest-dom/vitest'
import { transferableAbortController } from 'node:util'

// React Router's data router uses Node's Request in jsdom; its signal must
// come from the same implementation (jsdom's AbortSignal is incompatible).
globalThis.AbortController = transferableAbortController()
  .constructor as typeof AbortController
globalThis.AbortSignal = new AbortController().signal
  .constructor as typeof AbortSignal

// jsdom does not implement native dialog visibility/focus management.
Object.defineProperties(HTMLDialogElement.prototype, {
  showModal: {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = true
    },
  },
  close: {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = false
    },
  },
})
