interface Props {
  color: string
  title?: string
}

/** A small filled circle used as a status indicator next to a label. */
export function StatusDot({ color, title }: Props) {
  return (
    <span
      title={title}
      style={{
        display: 'inline-block',
        width: 9,
        height: 9,
        borderRadius: '50%',
        background: color,
        flexShrink: 0,
      }}
    />
  )
}
