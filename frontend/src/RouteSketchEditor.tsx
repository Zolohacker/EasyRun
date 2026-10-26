import {useEffect,useRef} from 'react'
import L,{type Map as LeafletMap,type LayerGroup} from 'leaflet'
import {LocateFixed,RotateCcw,Trash2,Undo2} from 'lucide-react'
import type {Area,Point} from './types'
import {routeDistance} from './manualRoutes'
import {routeMarkerIcon} from './routeMarkers'
import './route-markers.css'
import './routes.css'

type Props={area:Area;points:Point[];onChange:(points:Point[])=>void;minimumM:number;disabled?:boolean;preview?:boolean}
export default function RouteSketchEditor({area,points,onChange,minimumM,disabled=false,preview=false}:Props){
  const host=useRef<HTMLDivElement>(null),mapRef=useRef<LeafletMap|null>(null),layer=useRef<LayerGroup|null>(null)
  const latest=useRef({points,onChange,disabled});latest.current={points,onChange,disabled}
  const distance=routeDistance(points)
  const fit=()=>{const map=mapRef.current;if(!map)return;const shape=area.boundary||[];if(shape.length>=3)map.fitBounds(L.latLngBounds(shape.map(p=>[p.lat,p.lng])).pad(.12),{maxZoom:18});else map.setView([area.center.lat,area.center.lng],17)}
  useEffect(()=>{
    if(!host.current)return
    const opts={subdomains:['1','2','3','4'],maxZoom:18,maxNativeZoom:18,attribution:'&copy; 高德地图'}
    const road=L.tileLayer('https://webrd0{s}.is.autonavi.com/appmaptile?lang=zh_cn&size=1&scale=1&style=8&x={x}&y={y}&z={z}',opts)
    const satellite=L.layerGroup([
      L.tileLayer('https://webst0{s}.is.autonavi.com/appmaptile?style=6&x={x}&y={y}&z={z}',opts),
      L.tileLayer('https://webst0{s}.is.autonavi.com/appmaptile?style=8&x={x}&y={y}&z={z}',opts),
    ])
    const map=L.map(host.current,{center:[area.center.lat,area.center.lng],zoom:17,layers:[road],preferCanvas:true,doubleClickZoom:false})
    map.attributionControl.setPrefix(false)
    L.control.layers({'标准地图':road,'卫星影像':satellite},undefined,{position:'topright'}).addTo(map)
    mapRef.current=map;layer.current=L.layerGroup().addTo(map)
    if((area.boundary?.length||0)>=3)L.polygon(area.boundary!.map(p=>[p.lat,p.lng]),{color:'#647084',weight:2,dashArray:'6 6',fillColor:'#4f60ca',fillOpacity:.06,interactive:false}).addTo(map)
    fit()
    map.on('click',(e:L.LeafletMouseEvent)=>{const v=latest.current;if(!v.disabled&&v.points.length<2000)v.onChange([...v.points,{lat:e.latlng.lat,lng:e.latlng.lng}])})
    const observer=new ResizeObserver(()=>map.invalidateSize({animate:false}));observer.observe(host.current)
    return()=>{observer.disconnect();map.remove();mapRef.current=null;layer.current=null}
  },[area.id])
  useEffect(()=>{
    const group=layer.current;if(!group)return;group.clearLayers()
    if(points.length>1){L.polyline(points.map(p=>[p.lat,p.lng]),{color:'#fff',weight:9,interactive:false}).addTo(group);L.polyline(points.map(p=>[p.lat,p.lng]),{color:'#4f60ca',weight:4,interactive:false}).addTo(group)}
    points.forEach((p,index)=>{
      if(preview&&index!==0&&index!==points.length-1)return
      const kind=index===0?'start':index===points.length-1?'finish':'point'
      const label=kind==='start'?'起点':kind==='finish'?'终点':`节点 ${index+1}`
      const marker=L.marker([p.lat,p.lng],{draggable:!disabled,keyboard:true,bubblingMouseEvents:false,riseOnHover:true,zIndexOffset:kind==='point'?0:500,icon:L.divIcon(routeMarkerIcon(kind,index+1)),title:disabled?label:`${label} · 拖动调整，右键删除`,alt:label}).addTo(group)
      marker.on('dragend',()=>{const v=latest.current;const pos=marker.getLatLng();v.onChange(v.points.map((p,i)=>i===index?{lat:pos.lat,lng:pos.lng}:p))})
      marker.on('contextmenu',()=>{const v=latest.current;if(!v.disabled)v.onChange(v.points.filter((_,i)=>i!==index))})
    })
  },[points,disabled,preview,area.id])
  return <div className={`route-sketch ${preview?'sketch-preview':''}`}>
    <div className="sketch-heading"><div><strong>{(distance/1000).toFixed(3)} <small>km</small></strong><span>需大于 {(minimumM/1000).toFixed(2)} km · {points.length} 个节点</span></div><span className={`sketch-status ${distance>minimumM?'ready':''}`}>{distance>minimumM?'长度符合要求':'继续绘制'}</span></div>
    <div ref={host} className="sketch-map" aria-label={`${area.name}手绘路线地图`}/>
    <div className="sketch-toolbar"><button type="button" className="button subtle" onClick={fit}><LocateFixed/>定位区域</button><button type="button" className="button subtle" disabled={disabled||!points.length} onClick={()=>onChange(points.slice(0,-1))}><Undo2/>撤销一点</button><button type="button" className="button subtle" disabled={disabled||points.length<2} onClick={()=>onChange([...points].reverse())}><RotateCcw/>反转方向</button><button type="button" className="button subtle" disabled={disabled||!points.length} onClick={()=>onChange([])}><Trash2/>清空</button></div>
    <p className="portal-hint">点击地图添加节点，拖动节点调整；右键节点可删除。按绘制顺序从起点截取任务距离，不要求闭环。最多 2000 个节点。</p>
  </div>
}
