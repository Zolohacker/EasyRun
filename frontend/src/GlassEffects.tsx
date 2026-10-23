import {useEffect} from 'react'
import {attachGlassPointer} from './glassMotion'

export default function GlassEffects() {
  useEffect(()=>attachGlassPointer(document,window),[])
  return <div className="ambient-lights" aria-hidden="true"><span/><span/><span/></div>
}
