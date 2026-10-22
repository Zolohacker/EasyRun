import SearchSelect from './SearchSelect'
import {useEffect,useState,type ReactNode} from 'react'
import {ChevronLeft,ChevronRight} from 'lucide-react'

export type PageControls={page:number;pageSize:number;total:number;setPage:(page:number)=>void;setPageSize:(size:number)=>void}
export function usePagination<T>(items:T[],resetKey:unknown=''){
  const [page,setPage]=useState(1),[pageSize,setPageSize]=useState(10)
  const total=items.length,pages=Math.max(1,Math.ceil(total/pageSize)),current=Math.min(page,pages)
  useEffect(()=>setPage(1),[resetKey,pageSize])
  useEffect(()=>{if(page>pages)setPage(pages)},[page,pages])
  return {items:items.slice((current-1)*pageSize,current*pageSize),page:current,pageSize,total,setPage,setPageSize}
}
export function Pagination({page,pageSize,total,setPage,setPageSize}:PageControls){
  const pages=Math.max(1,Math.ceil(total/pageSize))
  return <footer className="list-pagination"><span>共 <b>{total}</b> 条{total>0&&` · ${(page-1)*pageSize+1}–${Math.min(page*pageSize,total)}`}</span><label>每页<SearchSelect aria-label="每页条数" value={pageSize} onChange={e=>{setPageSize(Number(e.target.value));setPage(1)}}>{[10,20,50].map(size=><option key={size} value={size}>{size} 条</option>)}</SearchSelect></label><div><button type="button" className="icon-button" aria-label="上一页" disabled={page<=1} onClick={()=>setPage(page-1)}><ChevronLeft size={16}/></button><span>{page} / {pages}</span><button type="button" className="icon-button" aria-label="下一页" disabled={page>=pages} onClick={()=>setPage(page+1)}><ChevronRight size={16}/></button></div></footer>
}
export function ListViewport({pagination,children,className=''}:{pagination:PageControls;children:ReactNode;className?:string}){
  return <div className="paginated-list"><div className={`list-viewport ${className}`}>{children}</div><Pagination {...pagination}/></div>
}
