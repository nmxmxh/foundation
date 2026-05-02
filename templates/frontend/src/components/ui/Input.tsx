import { forwardRef as reactForwardRef } from 'react'
import type { ReactNode } from 'react'
import {
  MinimalInput,
  type MinimalInputProps,
} from '@ovasabi/ui-minimal'

interface InputProps extends Omit<MinimalInputProps, 'prefix' | 'suffix'> {
  leftIcon?: ReactNode
  rightIcon?: ReactNode
  fullWidth?: boolean
}

export const Input = reactForwardRef<HTMLInputElement, InputProps>(
  ({ leftIcon, rightIcon, fullWidth: _fullWidth, ...props }, ref) => (
    <MinimalInput ref={ref} prefix={leftIcon} suffix={rightIcon} {...props} />
  ),
)

Input.displayName = 'Input'
