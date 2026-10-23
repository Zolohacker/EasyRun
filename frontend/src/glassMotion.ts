const surfaces = '.panel:not(.portal-save):not(.settings-savebar), .portal-balance-card, .portal-auth-card'

/** Pointer position is local to the surface; it is never persisted or transmitted. */
export function glassCoordinates(rect: {left:number;top:number;width:number;height:number}, x:number, y:number) {
  const percent=(value:number,size:number)=>size>0&&Number.isFinite(size)&&Number.isFinite(value)
    ? `${Math.round(Math.min(100,Math.max(0,value/size*100))*10)/10}%` : '50%'
  return {x:percent(x-rect.left,rect.width),y:percent(y-rect.top,rect.height)}
}

/** One delegated listener, one paint per frame, no React re-renders on pointer movement. */
export function attachGlassPointer(doc:Document, view:Window) {
  const preference=view.matchMedia('(hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference)')
  let active:HTMLElement|null=null
  let pending:{surface:HTMLElement;x:number;y:number}|null=null
  let frame=0,enabled=false

  const clearSurface=()=>{
    if(!active)return
    active.removeAttribute('data-glass-active')
    active.style.removeProperty('--glass-x')
    active.style.removeProperty('--glass-y')
    active=null
  }
  const reset=()=>{
    if(frame)view.cancelAnimationFrame(frame)
    frame=0;pending=null;clearSurface()
  }
  const paint=()=>{
    frame=0
    const next=pending;pending=null
    if(!next?.surface.isConnected){clearSurface();return}
    if(active!==next.surface){clearSurface();active=next.surface}
    const point=glassCoordinates(active.getBoundingClientRect(),next.x,next.y)
    active.style.setProperty('--glass-x',point.x)
    active.style.setProperty('--glass-y',point.y)
    active.setAttribute('data-glass-active','')
  }
  const move=(event:PointerEvent)=>{
    if(event.pointerType==='touch'){reset();return}
    const target=event.target as Element|null
    // The map and popovers own their pointer interaction; do not decorate them.
    const surface=typeof target?.closest==='function'&&!target.closest('.leaflet-container, .search-select-popup')
      ? target.closest<HTMLElement>(surfaces) : null
    pending=surface?{surface,x:event.clientX,y:event.clientY}:null
    if(!frame)frame=view.requestAnimationFrame(paint)
  }
  const toggle=()=>{
    const next=preference.matches
    if(enabled===next)return
    enabled=next
    if(enabled){
      doc.addEventListener('pointermove',move,{passive:true})
      doc.addEventListener('pointerleave',reset)
      view.addEventListener('blur',reset)
      view.addEventListener('scroll',reset,{capture:true,passive:true})
    }else{
      doc.removeEventListener('pointermove',move)
      doc.removeEventListener('pointerleave',reset)
      view.removeEventListener('blur',reset)
      view.removeEventListener('scroll',reset,true)
      reset()
    }
  }
  preference.addEventListener('change',toggle)
  toggle()
  return ()=>{
    preference.removeEventListener('change',toggle)
    doc.removeEventListener('pointermove',move)
    doc.removeEventListener('pointerleave',reset)
    view.removeEventListener('blur',reset)
    view.removeEventListener('scroll',reset,true)
    reset()
  }
}
