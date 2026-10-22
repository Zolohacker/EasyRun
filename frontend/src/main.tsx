import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './PortalRoot'
import 'leaflet/dist/leaflet.css'
import './styles.css'
import './workspace-layout.css'
import './mobile.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode><App /></StrictMode>,
)
