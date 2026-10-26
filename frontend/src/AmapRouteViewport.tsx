import { useCallback, useEffect, useRef } from 'react'
import L, { type LatLngBounds, type LayerGroup, type Map as LeafletMap } from 'leaflet'
import { LocateFixed, ScanLine } from 'lucide-react'
import type { Area, Route, Sample } from './types'
import {routeMarkerIcon} from './routeMarkers'
import './route-markers.css'

const DEFAULT_CENTER: L.LatLngExpression = [39.905, 116.397]
const TILE_OPTIONS: L.TileLayerOptions = {
  subdomains: ['1', '2', '3', '4'],
  minZoom: 3,
  maxZoom: 18,
  maxNativeZoom: 18,
}

type Props = {
  route: Route | null
  samples: Sample[]
  area?: Area
  variant?: 'plan' | 'record'
}

export default function AmapRouteViewport({ route, samples, area, variant = 'plan' }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const mapRef = useRef<LeafletMap | null>(null)
  const dataLayerRef = useRef<LayerGroup | null>(null)
  const visibleBoundsRef = useRef<LatLngBounds | null>(null)

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return

    const road = L.tileLayer(
      'https://webrd0{s}.is.autonavi.com/appmaptile?lang=zh_cn&size=1&scale=1&style=8&x={x}&y={y}&z={z}',
      {...TILE_OPTIONS, attribution: '&copy; 高德地图'},
    )
    const satellite = L.tileLayer(
      'https://webst0{s}.is.autonavi.com/appmaptile?style=6&x={x}&y={y}&z={z}',
      TILE_OPTIONS,
    )
    const satelliteLabels = L.tileLayer(
      'https://webst0{s}.is.autonavi.com/appmaptile?style=8&x={x}&y={y}&z={z}',
      {...TILE_OPTIONS, attribution: '&copy; 高德地图'},
    )
    const satelliteGroup = L.layerGroup([satellite, satelliteLabels])
    const map = L.map(containerRef.current, {
      attributionControl: true,
      center: DEFAULT_CENTER,
      zoom: 14,
      zoomControl: true,
      preferCanvas: true,
      layers: [road],
    })
    map.attributionControl.setPrefix(false)
    L.control.layers({'标准地图': road, '卫星影像': satelliteGroup}, undefined, {
      position: 'topright',
      collapsed: true,
    }).addTo(map)

    const dataLayer = L.layerGroup().addTo(map)
    mapRef.current = map
    dataLayerRef.current = dataLayer

    const resizeObserver = new ResizeObserver(() => map.invalidateSize({animate: false}))
    resizeObserver.observe(containerRef.current)
    window.setTimeout(() => map.invalidateSize({animate: false}), 0)

    return () => {
      resizeObserver.disconnect()
      map.remove()
      mapRef.current = null
      dataLayerRef.current = null
      visibleBoundsRef.current = null
    }
  }, [])

  useEffect(() => {
    const map = mapRef.current
    const dataLayer = dataLayerRef.current
    if (!map || !dataLayer) return
    dataLayer.clearLayers()

    const bounds = L.latLngBounds([])
    const boundary = (area?.boundary || []).map(point => [point.lat, point.lng] as L.LatLngTuple)
    if (boundary.length >= 3) {
      L.polygon(boundary, {
        color: '#647084',
        weight: 2,
        opacity: .9,
        dashArray: '7 7',
        fillColor: '#4f60ca',
        fillOpacity: .055,
      }).bindTooltip(`${area?.name || '校园'}围栏`, {sticky: true}).addTo(dataLayer)
      bounds.extend(L.latLngBounds(boundary))
    }

    const planned = (route?.points || []).map(point => [point.lat, point.lng] as L.LatLngTuple)
    if (planned.length >= 2) {
      L.polyline(planned, {
        color: '#ffffff',
        weight: 10,
        opacity: .9,
        lineCap: 'round',
        lineJoin: 'round',
      }).addTo(dataLayer)
      L.polyline(planned, {
        color: '#4f60ca',
        weight: 5,
        opacity: .98,
        lineCap: 'round',
        lineJoin: 'round',
      }).bindTooltip(`${variant === 'record' ? '实际轨迹' : '规划路线'} · ${(route!.distance_m / 1000).toFixed(2)} km`, {sticky: true}).addTo(dataLayer)
      bounds.extend(L.latLngBounds(planned))

      L.marker(planned[0], {icon: routeIcon('start'), zIndexOffset: 800})
        .bindTooltip('路线起点', {direction: 'top', offset: [0, -14]})
        .addTo(dataLayer)
      L.marker(planned[planned.length - 1], {icon: routeIcon('finish'), zIndexOffset: 790})
        .bindTooltip('路线终点', {direction: 'top', offset: [0, -14]})
        .addTo(dataLayer)
    }

    const observed = samples.map(sample => [sample.lat, sample.lng] as L.LatLngTuple)
    if (observed.length >= 2) {
      L.polyline(observed, {
        color: '#2f7c65',
        weight: 3,
        opacity: .9,
        dashArray: '2 7',
        lineCap: 'round',
      }).bindTooltip(`采样轨迹 · ${samples.length.toLocaleString()} 点`, {sticky: true}).addTo(dataLayer)
      bounds.extend(L.latLngBounds(observed))
    }

    if (bounds.isValid()) {
      visibleBoundsRef.current = bounds
      map.fitBounds(bounds.pad(.1), {padding: [30, 30], maxZoom: 18, animate: false})
    } else if (area?.center) {
      visibleBoundsRef.current = null
      map.setView([area.center.lat, area.center.lng], 17, {animate: false})
    }
  }, [area, route, samples, variant])

  const fitRoute = useCallback(() => {
    const map = mapRef.current
    const bounds = visibleBoundsRef.current
    if (map && bounds?.isValid()) map.fitBounds(bounds.pad(.1), {padding: [30, 30], maxZoom: 18})
  }, [])

  const locateArea = useCallback(() => {
    if (mapRef.current && area?.center) mapRef.current.setView([area.center.lat, area.center.lng], 17)
  }, [area])

  return <div className="route-viewport amap-viewport">
    <div ref={containerRef} className="amap-canvas" aria-label="高德地图路线预览"/>
    <div className="amap-toolbar">
      <button type="button" title="定位到所选校园区域" disabled={!area?.center} onClick={locateArea}><LocateFixed size={15}/><span>区域</span></button>
      <button type="button" title="显示完整路线" disabled={!route} onClick={fitRoute}><ScanLine size={15}/><span>全览</span></button>
    </div>
    <div className="map-provider"><i/>高德地图 <b>GCJ-02</b></div>
    {!route && <div className="map-empty map-empty-on-map"><strong>{variant === 'record' ? '暂无轨迹点' : area ? '区域已定位' : '等待选择区域'}</strong><span>{variant === 'record' ? '同步轨迹后显示路线' : area ? '完成规划后显示路线' : '请选择账号、计划与区域'}</span></div>}
    {route && <>
      <div className="map-legend"><span><i className="planned"/>{variant === 'record' ? '实际轨迹' : '规划路线'}</span>{samples.length > 1 && <span><i className="observed"/>采样轨迹</span>}{variant === 'plan' && <span><i className="boundary"/>校园围栏</span>}</div>
      <div className="map-coordinates"><span>{route.source.toUpperCase()} · {route.coordinate_system}</span><b>{route.points.length.toLocaleString()} POINTS</b></div>
    </>}
  </div>
}

function routeIcon(kind: 'start' | 'finish') {
  return L.divIcon(routeMarkerIcon(kind))
}
