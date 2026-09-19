import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'

export function renderPage(element: ReactElement, route = '/') {
  const router = createMemoryRouter([{ path: '*', element }], {
    initialEntries: [route],
  })
  return { ...render(<RouterProvider router={router} />), router }
}
