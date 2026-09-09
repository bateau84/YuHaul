import { createContext } from 'react'
import type { ToastType } from '../types'

export interface ToastContextValue {
  addToast: (message: string, type?: ToastType) => void
}

export const ToastContext = createContext<ToastContextValue | null>(null)
