import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './PortalRoot'
import GlassEffects from './GlassEffects'
import 'leaflet/dist/leaflet.css'
import './styles.css'
import './workspace-layout.css'
import './mobile.css'
import './versions.css'
import './visual-theme.css'
import './liquid-glass.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode><GlassEffects/><App /></StrictMode>,
)
