import type { ReactNode } from 'react'
import {
  MinimalButton,
  type MinimalButtonProps,
} from '@ovasabi/ui-minimal'

type ButtonVariant = 'primary' | 'secondary' | 'outline' | 'ghost' | 'danger'

interface ButtonProps
  extends Omit<MinimalButtonProps, 'variant' | 'tone' | 'loading' | 'leading' | 'trailing'> {
  variant?: ButtonVariant
  isLoading?: boolean
  leftIcon?: ReactNode
  rightIcon?: ReactNode
}

const variantMap: Record<
  ButtonVariant,
  Pick<MinimalButtonProps, 'variant' | 'tone'>
> = {
  primary: { variant: 'primary', tone: 'brand' },
  secondary: { variant: 'secondary', tone: 'neutral' },
  outline: { variant: 'secondary', tone: 'brand' },
  ghost: { variant: 'quiet', tone: 'neutral' },
  danger: { variant: 'primary', tone: 'danger' },
}

export function Button({
  variant = 'primary',
  isLoading = false,
  leftIcon,
  rightIcon,
  children,
  ...props
}: ButtonProps) {
  return (
    <MinimalButton
      {...variantMap[variant]}
      loading={isLoading}
      leading={leftIcon}
      trailing={rightIcon}
      {...props}
    >
      {children}
    </MinimalButton>
  )
}
