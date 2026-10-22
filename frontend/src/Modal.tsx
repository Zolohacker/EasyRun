import {useEffect,useId,useRef,type ReactNode} from 'react'
import {createPortal} from 'react-dom'
import {X} from 'lucide-react'

let locks=0
let previousOverflow=''
let previousPadding=''

export function useOverlayScrollLock(open:boolean) {
  useEffect(()=>{
    if(!open)return
    if(locks++===0){
      previousOverflow=document.body.style.overflow
      previousPadding=document.body.style.paddingRight
      const gutter=window.innerWidth-document.documentElement.clientWidth
      const padding=Number.parseFloat(getComputedStyle(document.body).paddingRight)||0
      document.body.style.overflow='hidden'
      if(gutter>0)document.body.style.paddingRight=`${padding+gutter}px`
    }
    return()=>{if(--locks===0){document.body.style.overflow=previousOverflow;document.body.style.paddingRight=previousPadding}}
  },[open])
}

export default function Modal({title,onClose,children,wide=false,className=''}:{title:string;onClose:()=>void;children:ReactNode;wide?:boolean;className?:string}) {
  const titleID=useId(),dialog=useRef<HTMLElement>(null),backdrop=useRef<HTMLDivElement>(null)
  const close=useRef(onClose);close.current=onClose
  useOverlayScrollLock(true)
  useEffect(()=>{
    const previous=document.activeElement as HTMLElement|null
    const focusable=()=>Array.from(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled),a[href],input:not(:disabled):not([type="hidden"]),textarea:not(:disabled),select:not(:disabled):not([tabindex="-1"]),[tabindex]:not([tabindex="-1"])')||[]).filter(el=>el.getClientRects().length>0)
    if(!dialog.current?.contains(document.activeElement))(focusable()[0]||dialog.current)?.focus()
    const key=(event:KeyboardEvent)=>{
      if(event.defaultPrevented)return
      if(event.key==='Escape'){event.preventDefault();close.current();return}
      if(event.key!=='Tab'||document.querySelector('.search-select-popup')?.contains(document.activeElement))return
      const elements=focusable(),first=elements[0],last=elements[elements.length-1]
      if(!first){event.preventDefault();dialog.current?.focus()}
      else if(!dialog.current?.contains(document.activeElement)){event.preventDefault();first.focus()}
      else if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus()}
      else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus()}
    }
    document.addEventListener('keydown',key)
    return()=>{document.removeEventListener('keydown',key);if(previous?.isConnected)previous.focus()}
  },[])
  useEffect(()=>{
    const viewport=window.visualViewport
    const place=()=>{
      backdrop.current?.style.setProperty('--dialog-height',`${viewport?.height||window.innerHeight}px`)
      backdrop.current?.style.setProperty('--dialog-top',`${viewport?.offsetTop||0}px`)
    }
    place();viewport?.addEventListener('resize',place);viewport?.addEventListener('scroll',place);window.addEventListener('resize',place)
    return()=>{viewport?.removeEventListener('resize',place);viewport?.removeEventListener('scroll',place);window.removeEventListener('resize',place)}
  },[])
  return createPortal(<div ref={backdrop} className="modal-backdrop" onPointerDown={event=>{if(event.target===event.currentTarget)close.current()}}>
    <section ref={dialog} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby={titleID} className={`modal app-modal ${wide?'modal-wide':''} ${className}`.trim()}>
      <div className="modal-head"><h2 id={titleID}>{title}</h2><button type="button" className="icon-button" aria-label="关闭" title="关闭" onClick={()=>close.current()}><X size={19}/></button></div>
      <div className="modal-body">{children}</div>
    </section>
  </div>,document.body)
}
