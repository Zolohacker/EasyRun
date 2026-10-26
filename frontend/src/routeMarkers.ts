// Leaflet owns the outer translate3d; the inner badge owns appearance. Keep
// labels unrotated and use an exact half-size anchor for every marker size.
export function routeMarkerIcon(kind:'start'|'finish'|'point',index=1){
  const number=Math.max(1,Math.floor(Number.isFinite(index)?index:1))
  const label=kind==='start'?'起':kind==='finish'?'终':String(number)
  const size=kind!=='point'?20:label.length>=4?22:label.length>=3?18:14
  return {
    className:`route-node-marker route-node-${kind}`,
    html:`<span class="route-node-badge"><span class="route-node-label">${label}</span></span>`,
    iconSize:[size,size] as [number,number],
    iconAnchor:[size/2,size/2] as [number,number],
  }
}
