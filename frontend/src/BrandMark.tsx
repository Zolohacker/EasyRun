/** A route-shaped R, shared by the workspace, sign-in and public upload pages. */
export default function BrandMark({className = ''}: {className?: string}) {
  return <svg className={`brand-symbol ${className}`} viewBox="0 0 32 32" fill="none" aria-hidden="true">
    <path d="M8 25V9a3 3 0 0 1 3-3h9a5 5 0 0 1 0 10H8m8 0 8 9" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round"/>
    <circle cx="8" cy="25" r="2" fill="currentColor"/>
  </svg>
}
