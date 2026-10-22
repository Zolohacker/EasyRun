import {Children,isValidElement,useEffect,useId,useMemo,useRef,useState,type ChangeEvent,type ReactNode,type SelectHTMLAttributes} from 'react'
import {createPortal} from 'react-dom'
import {Check,ChevronDown,Search} from 'lucide-react'

type Option={value:string;label:string;disabled:boolean}
function textOf(node:ReactNode):string{return Children.toArray(node).map(child=>isValidElement<{children?:ReactNode}>(child)?textOf(child.props.children):String(child)).join('')}
function optionsOf(children:ReactNode,disabled=false):Option[]{
  return Children.toArray(children).flatMap(child=>{
    if(!isValidElement<{value?:string|number;disabled?:boolean;children?:ReactNode}>(child))return []
    if(child.type==='option')return [{value:String(child.props.value??textOf(child.props.children)),label:textOf(child.props.children),disabled:disabled||!!child.props.disabled}]
    return optionsOf(child.props.children,disabled||!!child.props.disabled)
  })
}

// A drop-in single-value select. The native control only supplies form semantics;
// all visible selection, search and keyboard interaction use the custom popup.
export default function SearchSelect({children,value,defaultValue,onChange,disabled,className='',style,...props}:SelectHTMLAttributes<HTMLSelectElement>){
  const id=useId(),native=useRef<HTMLSelectElement>(null),button=useRef<HTMLButtonElement>(null),popup=useRef<HTMLDivElement>(null),search=useRef<HTMLInputElement>(null)
  const options=useMemo(()=>optionsOf(children),[children])
  const [ownValue,setOwnValue]=useState(String(defaultValue??'')),[open,setOpen]=useState(false),[query,setQuery]=useState(''),[active,setActive]=useState(0),[label,setLabel]=useState(''),[invalid,setInvalid]=useState(false)
  const [position,setPosition]=useState<{left:number;top?:number;bottom?:number;width:number;maxHeight:number}>({left:0,top:0,width:240,maxHeight:300})
  const selected=String(value??ownValue),choice=options.find(option=>option.value===selected)
  const matches=options.filter(option=>(option.label+' '+option.value).toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))
  const visible=matches.slice(0,150)
  useEffect(()=>{setLabel(button.current?.closest('label')?.querySelector('span')?.textContent||'请选择')},[])
  useEffect(()=>{if(disabled)setOpen(false)},[disabled])
  useEffect(()=>{
    if(!open)return
    const place=()=>{
      const rect=button.current?.getBoundingClientRect();if(!rect)return
      const width=Math.min(Math.max(rect.width,220),window.innerWidth-24)
      const below=window.innerHeight-rect.bottom-16,above=rect.top-16,flip=below<240&&above>below
      const maxHeight=Math.max(130,Math.min(340,flip?above:below))
      setPosition({left:Math.max(12,Math.min(rect.left,window.innerWidth-width-12)),top:flip?undefined:rect.bottom+6,bottom:flip?window.innerHeight-rect.top+6:undefined,width,maxHeight})
    }
    const outside=(event:PointerEvent)=>{const target=event.target as Node;if(!button.current?.contains(target)&&!popup.current?.contains(target))setOpen(false)}
    const scrolled=(event:Event)=>{if(!popup.current?.contains(event.target as Node))setOpen(false)}
    place();search.current?.focus()
    document.addEventListener('pointerdown',outside);window.addEventListener('resize',place);window.addEventListener('scroll',scrolled,true)
    return()=>{document.removeEventListener('pointerdown',outside);window.removeEventListener('resize',place);window.removeEventListener('scroll',scrolled,true)}
  },[open])
  useEffect(()=>{if(open)popup.current?.querySelector(`[data-option-index="${active}"]`)?.scrollIntoView({block:'nearest'})},[active,open])
  const choose=(option:Option)=>{
    if(option.disabled||!native.current)return
    native.current.value=option.value
    const event={target:native.current,currentTarget:native.current,type:'change',preventDefault(){},stopPropagation(){}} as ChangeEvent<HTMLSelectElement>
    onChange?.(event)
    setOwnValue(native.current.value);setInvalid(false);setOpen(false);button.current?.focus()
  }
  const show=()=>{setQuery('');setActive(Math.max(0,options.findIndex(option=>option.value===selected)));setOpen(true)}
  return <div className={`search-select ${className} ${open?'is-open':''} ${invalid?'is-invalid':''}`} style={style}>
    <select {...props} ref={native} value={selected} disabled={disabled} onChange={onChange} tabIndex={-1} aria-hidden="true" className="search-select-native" onInvalid={event=>{event.preventDefault();setInvalid(true);show();button.current?.focus()}}>{children}</select>
    <button ref={button} type="button" role="combobox" aria-label={props['aria-label']||label} aria-expanded={open} aria-controls={`${id}-options`} aria-haspopup="listbox" aria-required={props.required} disabled={disabled} title={choice?.label} onClick={event=>{event.preventDefault();if(open)setOpen(false);else show()}} onKeyDown={event=>{if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();show()}else if(event.key==='Escape')setOpen(false)}}><span className={selected?'':'placeholder'}>{choice?.label||'请选择'}</span><ChevronDown size={16}/></button>
    {open&&createPortal(<div ref={popup} className="search-select-popup" style={position} onKeyDown={event=>{
      if(event.key==='Escape'){event.preventDefault();setOpen(false);button.current?.focus()}
      else if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();setActive(index=>Math.max(0,Math.min(visible.length-1,index+(event.key==='ArrowDown'?1:-1))))}
      else if(event.key==='Enter'){event.preventDefault();if(visible[active])choose(visible[active])}
      else if(event.key==='Tab')setOpen(false)
    }}><div className="search-select-search"><Search size={16}/><input ref={search} type="search" aria-label="搜索选项" placeholder="输入关键词搜索…" autoComplete="off" value={query} onChange={event=>{setQuery(event.target.value);setActive(0)}}/></div><div role="listbox" id={`${id}-options`} aria-label={props['aria-label']||label} className="search-select-options">{visible.map((option,index)=><button key={option.value} type="button" role="option" aria-selected={option.value===selected} aria-disabled={option.disabled} disabled={option.disabled} data-option-index={index} className={index===active?'active':''} onMouseEnter={()=>setActive(index)} onClick={()=>choose(option)}><span>{option.label}</span>{option.value===selected&&<Check size={16}/>}</button>)}{!visible.length&&<p>没有匹配的选项</p>}{matches.length>150&&<small>仅显示前 150 项，请搜索缩小范围。</small>}</div></div>,document.body)}
  </div>
}
