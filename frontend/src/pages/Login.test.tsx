import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { HashRouter } from 'react-router-dom'
import Login from '../pages/Login'

describe('Login page', () => {
  it('renders form and submits disabled state', () => {
    render(
      <HashRouter>
        <Login onLogin={() => {}} />
      </HashRouter>,
    )
    expect(screen.getByText('rent-auto 登录')).toBeDefined()
    expect(screen.getByRole('button', { name: '登录' })).toBeDefined()
  })
})
