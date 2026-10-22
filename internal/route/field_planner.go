package route

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Zolohacker/EasyRun/internal/geo"
)

// The public walking network often omits private athletics tracks. In that
// case the boundary itself is enough to construct a deterministic, routable
// field graph: several smoothed inset contours form track-like corridors and
// automatic cross-links allow a seeded planner to change corridors. Start and
// server checkpoint coordinates are attached as exact graph vertices.

type fieldXY struct {
	X float64
	Y float64
}

type fieldProjection struct {
	origin geo.Point
	cosLat float64
}

func newFieldProjection(origin geo.Point) fieldProjection {
	cosLat := math.Cos(origin.Lat * math.Pi / 180)
	if math.Abs(cosLat) < 1e-8 {
		cosLat = 1e-8
	}
	return fieldProjection{origin: origin, cosLat: cosLat}
}

func (p fieldProjection) toXY(point geo.Point) fieldXY {
	const radius = 6371008.8
	return fieldXY{
		X: (point.Lng - p.origin.Lng) * math.Pi / 180 * radius * p.cosLat,
		Y: (point.Lat - p.origin.Lat) * math.Pi / 180 * radius,
	}
}

func (p fieldProjection) toGeo(point fieldXY) geo.Point {
	return geo.OffsetMeters(p.origin, point.X, point.Y)
}

type fieldNode struct {
	Point geo.Point
	XY    fieldXY
}

type fieldEdge struct {
	To      int
	LengthM float64
}

type automaticFieldGraph struct {
	projection fieldProjection
	boundary   []fieldXY
	nodes      []fieldNode
	edges      [][]fieldEdge
	routeNodes []int
	required   []int
}

func planAutomaticFieldRoute(ctx context.Context, request PlanRequest, count int, routeSeed int64) (Route, error) {
	graph, err := buildAutomaticFieldGraph(request)
	if err != nil {
		return Route{}, fmt.Errorf("automatic field-network construction: %w", err)
	}
	routes := make([]Route, 0, count)
	var firstErr error
	for candidate := 0; candidate < count; candidate++ {
		if err := ctx.Err(); err != nil {
			return Route{}, err
		}
		planned, planErr := graph.planCandidate(request, routeSeed, candidate)
		if planErr != nil {
			if firstErr == nil {
				firstErr = planErr
			}
			continue
		}
		routes = append(routes, planned)
	}
	if len(routes) == 0 {
		if firstErr == nil {
			firstErr = errors.New("no automatic field route candidate succeeded")
		}
		return Route{}, firstErr
	}
	sort.SliceStable(routes, func(left, right int) bool {
		return routes[left].Quality.Score < routes[right].Quality.Score
	})
	return routes[0], nil
}

func buildAutomaticFieldGraph(request PlanRequest) (*automaticFieldGraph, error) {
	if len(request.Boundary) < 3 {
		return nil, errors.New("a field network requires a boundary")
	}
	projection := newFieldProjection(request.Start)
	boundary := make([]fieldXY, 0, len(request.Boundary))
	for _, point := range request.Boundary {
		boundary = append(boundary, projection.toXY(point))
	}
	graph := &automaticFieldGraph{projection: projection, boundary: boundary}

	rings := graph.buildInsetRings()
	if len(rings) == 0 {
		rings = graph.buildHexFallback()
	}
	if len(graph.routeNodes) < 6 || len(rings) == 0 {
		return nil, errors.New("boundary produced too few connected field-network nodes")
	}
	graph.connectRings(rings)

	requiredPoints := append([]geo.Point{request.Start}, request.Via...)
	for index, point := range requiredPoints {
		xy := projection.toXY(point)
		if !insideOrBoundaryXY(xy, boundary, 1.5) {
			return nil, fmt.Errorf("required checkpoint %d is outside the field boundary", index)
		}
		node, err := graph.attachRequired(point)
		if err != nil {
			return nil, fmt.Errorf("attach required checkpoint %d: %w", index, err)
		}
		graph.required = append(graph.required, node)
	}
	for index := 1; index < len(graph.required); index++ {
		distances, previous, err := graph.shortestTree(graph.required[index-1], nil, 1, index, false)
		if err != nil {
			return nil, fmt.Errorf("required checkpoint %d is disconnected: %w", index, err)
		}
		if math.IsInf(distances[graph.required[index]], 1) {
			return nil, fmt.Errorf("required checkpoint %d is disconnected", index)
		}
		if _, err := reconstructFieldPath(previous, graph.required[index-1], graph.required[index]); err != nil {
			return nil, fmt.Errorf("required checkpoint %d is disconnected: %w", index, err)
		}
	}
	return graph, nil
}

func (g *automaticFieldGraph) buildInsetRings() [][]int {
	if len(g.boundary) < 3 {
		return nil
	}
	mean := fieldXY{}
	for _, point := range g.boundary {
		mean.X += point.X
		mean.Y += point.Y
	}
	mean.X /= float64(len(g.boundary))
	mean.Y /= float64(len(g.boundary))

	var covarianceXX, covarianceYY, covarianceXY float64
	for _, point := range g.boundary {
		x, y := point.X-mean.X, point.Y-mean.Y
		covarianceXX += x * x
		covarianceYY += y * y
		covarianceXY += x * y
	}
	angle := 0.5 * math.Atan2(2*covarianceXY, covarianceXX-covarianceYY)
	cosine, sine := math.Cos(angle), math.Sin(angle)
	rotated := make([]fieldXY, len(g.boundary))
	minU, maxU := math.Inf(1), math.Inf(-1)
	minV, maxV := math.Inf(1), math.Inf(-1)
	for index, point := range g.boundary {
		x, y := point.X-mean.X, point.Y-mean.Y
		u, v := x*cosine+y*sine, -x*sine+y*cosine
		rotated[index] = fieldXY{X: u, Y: v}
		minU, maxU = math.Min(minU, u), math.Max(maxU, u)
		minV, maxV = math.Min(minV, v), math.Max(maxV, v)
	}
	width, height := maxU-minU, maxV-minV
	minorHalf := math.Min(width, height) / 2
	if minorHalf < 8 {
		return nil
	}
	maxInset := math.Min(28, minorHalf*0.44)
	gap := math.Max(4, math.Min(7, maxInset/4))
	offsets := make([]float64, 0, 5)
	for inset := math.Min(4, maxInset*0.35); inset <= maxInset+0.01 && len(offsets) < 5; inset += gap {
		offsets = append(offsets, inset)
	}
	if len(offsets) == 1 && maxInset-offsets[0] >= 2 {
		offsets = append(offsets, maxInset)
	}

	midU, midV := (minU+maxU)/2, (minV+maxV)/2
	rings := make([][]int, 0, len(offsets))
	for _, inset := range offsets {
		scaleU := 1 - 2*inset/width
		scaleV := 1 - 2*inset/height
		if scaleU <= 0.1 || scaleV <= 0.1 {
			continue
		}
		insetPolygon := make([]fieldXY, len(rotated))
		for index, point := range rotated {
			u := midU + (point.X-midU)*scaleU
			v := midV + (point.Y-midV)*scaleV
			insetPolygon[index] = fieldXY{
				X: mean.X + u*cosine - v*sine,
				Y: mean.Y + u*sine + v*cosine,
			}
		}
		smoothed := chaikinClosed(insetPolygon, 2)
		samples := sampleClosedFieldLine(smoothed, 10)
		if len(samples) < 6 || !g.validClosedLine(samples) {
			continue
		}
		ring := make([]int, 0, len(samples))
		for _, point := range samples {
			ring = append(ring, g.addNode(g.projection.toGeo(point), point, true))
		}
		for index := range ring {
			g.addEdge(ring[index], ring[(index+1)%len(ring)])
		}
		rings = append(rings, ring)
	}
	return rings
}

func (g *automaticFieldGraph) buildHexFallback() [][]int {
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, point := range g.boundary {
		minX, maxX = math.Min(minX, point.X), math.Max(maxX, point.X)
		minY, maxY = math.Min(minY, point.Y), math.Max(maxY, point.Y)
	}
	areaEstimate := math.Max(1, (maxX-minX)*(maxY-minY))
	spacing := math.Max(10, math.Sqrt(areaEstimate/450))
	vertical := spacing * math.Sqrt(3) / 2
	nodes := make([]int, 0, 450)
	row := 0
	for y := minY; y <= maxY && len(nodes) < 600; y, row = y+vertical, row+1 {
		offset := 0.0
		if row%2 == 1 {
			offset = spacing / 2
		}
		for x := minX + offset; x <= maxX && len(nodes) < 600; x += spacing {
			point := fieldXY{X: x, Y: y}
			if !insideOrBoundaryXY(point, g.boundary, 0.2) || distanceToPolygonXY(point, g.boundary) < 2 {
				continue
			}
			nodes = append(nodes, g.addNode(g.projection.toGeo(point), point, true))
		}
	}
	for leftIndex, left := range nodes {
		for _, right := range nodes[leftIndex+1:] {
			distance := fieldXYDistance(g.nodes[left].XY, g.nodes[right].XY)
			if distance <= spacing*1.2 && g.segmentInside(g.nodes[left].XY, g.nodes[right].XY) {
				g.addEdge(left, right)
			}
		}
	}
	if len(nodes) == 0 {
		return nil
	}
	return [][]int{nodes}
}

func (g *automaticFieldGraph) connectRings(rings [][]int) {
	for ringIndex := 0; ringIndex+1 < len(rings); ringIndex++ {
		outer, inner := rings[ringIndex], rings[ringIndex+1]
		stride := maxInt(1, len(outer)/14)
		for index := 0; index < len(outer); index += stride {
			from := outer[index]
			nearest, nearestDistance := -1, math.Inf(1)
			for _, candidate := range inner {
				distance := fieldXYDistance(g.nodes[from].XY, g.nodes[candidate].XY)
				if distance < nearestDistance {
					nearest, nearestDistance = candidate, distance
				}
			}
			if nearest >= 0 && g.segmentInside(g.nodes[from].XY, g.nodes[nearest].XY) {
				g.addEdge(from, nearest)
			}
		}
	}
}

func (g *automaticFieldGraph) attachRequired(point geo.Point) (int, error) {
	xy := g.projection.toXY(point)
	for _, index := range g.routeNodes {
		if fieldXYDistance(xy, g.nodes[index].XY) <= 0.01 {
			return index, nil
		}
	}
	node := g.addNode(point, xy, false)
	type candidate struct {
		index    int
		distance float64
	}
	candidates := make([]candidate, 0, len(g.routeNodes))
	for _, routeNode := range g.routeNodes {
		candidates = append(candidates, candidate{index: routeNode, distance: fieldXYDistance(xy, g.nodes[routeNode].XY)})
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].distance < candidates[right].distance })
	connections := 0
	for _, candidate := range candidates {
		if connections >= 8 {
			break
		}
		if candidate.distance <= 0.05 || !g.segmentInside(xy, g.nodes[candidate.index].XY) {
			continue
		}
		g.addEdge(node, candidate.index)
		connections++
	}
	if connections == 0 {
		return -1, errors.New("no visible field-network connector")
	}
	return node, nil
}

func (g *automaticFieldGraph) addNode(point geo.Point, xy fieldXY, routeNode bool) int {
	index := len(g.nodes)
	g.nodes = append(g.nodes, fieldNode{Point: point, XY: xy})
	g.edges = append(g.edges, nil)
	if routeNode {
		g.routeNodes = append(g.routeNodes, index)
	}
	return index
}

func (g *automaticFieldGraph) addEdge(left, right int) {
	if left < 0 || right < 0 || left == right {
		return
	}
	for _, edge := range g.edges[left] {
		if edge.To == right {
			return
		}
	}
	length := fieldXYDistance(g.nodes[left].XY, g.nodes[right].XY)
	if length < 0.05 {
		return
	}
	g.edges[left] = append(g.edges[left], fieldEdge{To: right, LengthM: length})
	g.edges[right] = append(g.edges[right], fieldEdge{To: left, LengthM: length})
}

func (g *automaticFieldGraph) validClosedLine(points []fieldXY) bool {
	for index, point := range points {
		if !insideOrBoundaryXY(point, g.boundary, 0.5) ||
			!g.segmentInside(point, points[(index+1)%len(points)]) {
			return false
		}
	}
	return true
}

func (g *automaticFieldGraph) segmentInside(start, end fieldXY) bool {
	distance := fieldXYDistance(start, end)
	steps := maxInt(1, int(math.Ceil(distance/2)))
	for step := 0; step <= steps; step++ {
		fraction := float64(step) / float64(steps)
		point := fieldXY{X: start.X + (end.X-start.X)*fraction, Y: start.Y + (end.Y-start.Y)*fraction}
		if !insideOrBoundaryXY(point, g.boundary, 0.6) {
			return false
		}
	}
	return true
}

func (g *automaticFieldGraph) planCandidate(request PlanRequest, routeSeed int64, candidate int) (Route, error) {
	seed := routeSeed + int64(candidate+1)*104729
	usedEdges := make(map[[2]int]int)
	nodeUses := make([]int, len(g.nodes))
	points := []geo.Point{request.Start}
	current := g.required[0]
	stage := 0

	for checkpointIndex := 1; checkpointIndex < len(g.required); checkpointIndex++ {
		target := g.required[checkpointIndex]
		_, previous, err := g.shortestTree(current, usedEdges, seed, stage, false)
		if err != nil {
			return Route{}, fmt.Errorf("candidate %d checkpoint %d: %w", candidate+1, checkpointIndex, err)
		}
		path, err := reconstructFieldPath(previous, current, target)
		if err != nil {
			return Route{}, fmt.Errorf("candidate %d checkpoint %d: %w", candidate+1, checkpointIndex, err)
		}
		points = g.appendPath(points, path)
		markFieldPath(path, usedEdges, nodeUses)
		current = target
		stage++
	}

	mandatoryDistance := geo.PolylineDistance(points)
	maximumDistance := request.TargetDistanceM + 50
	if mandatoryDistance > maximumDistance+0.05 {
		return Route{}, fmt.Errorf("checkpoint route is %.1f m, beyond the allowed maximum %.1f m", mandatoryDistance, maximumDistance)
	}
	headroom := maximumDistance - request.TargetDistanceM
	overrun := math.Min(headroom, float64(8+(candidate*13)%35))
	desiredDistance := math.Max(request.TargetDistanceM+overrun, mandatoryDistance)
	geometryTarget := desiredDistance + math.Max(8, desiredDistance*0.003)

	lastDestination := -1
	for geo.PolylineDistance(points) < geometryTarget {
		if stage >= 96 {
			return Route{}, fmt.Errorf("candidate %d field graph did not reach the selected distance", candidate+1)
		}
		distances, previous, err := g.shortestTree(current, usedEdges, seed, stage, true)
		if err != nil {
			return Route{}, fmt.Errorf("candidate %d field leg %d: %w", candidate+1, stage+1, err)
		}
		remaining := geometryTarget - geo.PolylineDistance(points)
		destination, path, ok := g.chooseDestination(current, lastDestination, distances, previous, nodeUses, seed, stage, remaining)
		if !ok {
			return Route{}, fmt.Errorf("candidate %d field graph has no reachable destination", candidate+1)
		}
		points = g.appendPath(points, path)
		markFieldPath(path, usedEdges, nodeUses)
		current, lastDestination = destination, destination
		stage++
	}

	dense, err := densifyPreservingVertices(points, 5)
	if err != nil {
		return Route{}, fmt.Errorf("candidate %d field geometry: %w", candidate+1, err)
	}
	dense, err = clipPolylineDistance(dense, desiredDistance)
	if err != nil {
		return Route{}, fmt.Errorf("candidate %d field distance fitting: %w", candidate+1, err)
	}
	fittedDistance := geo.PolylineDistance(dense)
	retrace := retraceRatio(dense, 12)
	outside := outsideRatio(dense, request.Boundary)
	errorPct := math.Abs(fittedDistance-request.TargetDistanceM) / request.TargetDistanceM * 100
	score := errorPct + retrace*35 + outside*220
	source := "auto_field_graph"
	if len(request.Via) > 0 {
		source = "auto_field_graph_via"
	}
	return Route{
		ID: newID("route"), Source: source, CoordinateSystem: "provider_native", GeneratedAt: time.Now().UTC(),
		TargetDistanceM: request.TargetDistanceM, DistanceM: fittedDistance, EstimatedDurationS: fittedDistance / 1.5,
		Points: dense,
		Quality: Quality{
			DistanceErrorPct: round(errorPct, 3), RetraceRatio: round(retrace, 4),
			OutsideAreaRatio: round(outside, 4), Score: round(score, 3),
		},
	}, nil
}

func (g *automaticFieldGraph) chooseDestination(
	current, lastDestination int,
	distances []float64,
	previous []int,
	nodeUses []int,
	seed int64,
	stage int,
	remaining float64,
) (int, []int, bool) {
	nominal := math.Min(750, math.Max(100, remaining/2.3))
	if remaining < 100 {
		nominal = math.Max(35, remaining*1.2)
	}
	bestNode, bestScore := -1, math.Inf(1)
	var bestPath []int
	for _, candidate := range g.routeNodes {
		if candidate == current || candidate == lastDestination || math.IsInf(distances[candidate], 1) {
			continue
		}
		path, err := reconstructFieldPath(previous, current, candidate)
		if err != nil || len(path) < 2 {
			continue
		}
		length := g.pathDistance(path)
		if length < 20 {
			continue
		}
		shortfallPenalty := 0.0
		if remaining < 100 && length < remaining+3 {
			shortfallPenalty = (remaining + 3 - length) * 3
		}
		jitter := (fieldNoise(seed, stage, current, candidate) - 0.5) * nominal * 0.32
		score := math.Abs(length-nominal) + float64(nodeUses[candidate])*nominal*0.35 + shortfallPenalty + jitter
		if score < bestScore {
			bestNode, bestScore, bestPath = candidate, score, path
		}
	}
	return bestNode, bestPath, bestNode >= 0
}

func (g *automaticFieldGraph) appendPath(points []geo.Point, path []int) []geo.Point {
	for _, index := range path[1:] {
		points = append(points, g.nodes[index].Point)
	}
	return points
}

func (g *automaticFieldGraph) pathDistance(path []int) float64 {
	var total float64
	for index := 1; index < len(path); index++ {
		total += fieldXYDistance(g.nodes[path[index-1]].XY, g.nodes[path[index]].XY)
	}
	return total
}

func markFieldPath(path []int, usedEdges map[[2]int]int, nodeUses []int) {
	for index, node := range path {
		nodeUses[node]++
		if index > 0 {
			usedEdges[fieldEdgeKey(path[index-1], node)]++
		}
	}
}

func (g *automaticFieldGraph) shortestTree(
	start int,
	usedEdges map[[2]int]int,
	seed int64,
	stage int,
	penalizeReuse bool,
) ([]float64, []int, error) {
	if start < 0 || start >= len(g.nodes) {
		return nil, nil, errors.New("invalid graph start")
	}
	distances := make([]float64, len(g.nodes))
	previous := make([]int, len(g.nodes))
	for index := range distances {
		distances[index] = math.Inf(1)
		previous[index] = -1
	}
	distances[start] = 0
	queue := &fieldPriorityQueue{{node: start, distance: 0}}
	heap.Init(queue)
	for queue.Len() > 0 {
		item := heap.Pop(queue).(fieldQueueItem)
		if item.distance > distances[item.node]+1e-9 {
			continue
		}
		for _, edge := range g.edges[item.node] {
			noise := fieldNoise(seed, stage, item.node, edge.To)
			weight := edge.LengthM * (1 + noise*0.06)
			if penalizeReuse {
				weight += edge.LengthM * float64(usedEdges[fieldEdgeKey(item.node, edge.To)]) * 1.15
			}
			candidate := item.distance + weight
			if candidate+1e-9 < distances[edge.To] {
				distances[edge.To] = candidate
				previous[edge.To] = item.node
				heap.Push(queue, fieldQueueItem{node: edge.To, distance: candidate})
			}
		}
	}
	return distances, previous, nil
}

func reconstructFieldPath(previous []int, start, target int) ([]int, error) {
	if start == target {
		return []int{start}, nil
	}
	path := make([]int, 0, len(previous))
	for current, steps := target, 0; current >= 0 && steps <= len(previous); current, steps = previous[current], steps+1 {
		path = append(path, current)
		if current == start {
			for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
				path[left], path[right] = path[right], path[left]
			}
			return path, nil
		}
	}
	return nil, errors.New("target is unreachable")
}

func chaikinClosed(points []fieldXY, iterations int) []fieldXY {
	result := append([]fieldXY(nil), points...)
	for iteration := 0; iteration < iterations && len(result) >= 3; iteration++ {
		next := make([]fieldXY, 0, len(result)*2)
		for index, left := range result {
			right := result[(index+1)%len(result)]
			next = append(next,
				fieldXY{X: left.X*0.75 + right.X*0.25, Y: left.Y*0.75 + right.Y*0.25},
				fieldXY{X: left.X*0.25 + right.X*0.75, Y: left.Y*0.25 + right.Y*0.75},
			)
		}
		result = next
	}
	return result
}

func sampleClosedFieldLine(points []fieldXY, spacingM float64) []fieldXY {
	if len(points) < 3 {
		return nil
	}
	result := make([]fieldXY, 0, len(points)*2)
	for index, start := range points {
		end := points[(index+1)%len(points)]
		steps := maxInt(1, int(math.Ceil(fieldXYDistance(start, end)/spacingM)))
		for step := 0; step < steps; step++ {
			fraction := float64(step) / float64(steps)
			result = append(result, fieldXY{
				X: start.X + (end.X-start.X)*fraction,
				Y: start.Y + (end.Y-start.Y)*fraction,
			})
		}
	}
	return result
}

func insideOrBoundaryXY(point fieldXY, polygon []fieldXY, toleranceM float64) bool {
	if pointInPolygonXY(point, polygon) {
		return true
	}
	return distanceToPolygonXY(point, polygon) <= toleranceM
}

func pointInPolygonXY(point fieldXY, polygon []fieldXY) bool {
	inside := false
	for index, previous := 0, len(polygon)-1; index < len(polygon); previous, index = index, index+1 {
		left, right := polygon[index], polygon[previous]
		if (left.Y > point.Y) != (right.Y > point.Y) &&
			point.X < (right.X-left.X)*(point.Y-left.Y)/(right.Y-left.Y)+left.X {
			inside = !inside
		}
	}
	return inside
}

func distanceToPolygonXY(point fieldXY, polygon []fieldXY) float64 {
	minimum := math.Inf(1)
	for index, start := range polygon {
		end := polygon[(index+1)%len(polygon)]
		minimum = math.Min(minimum, distanceToSegmentXY(point, start, end))
	}
	return minimum
}

func distanceToSegmentXY(point, start, end fieldXY) float64 {
	dx, dy := end.X-start.X, end.Y-start.Y
	denominator := dx*dx + dy*dy
	if denominator <= 1e-12 {
		return fieldXYDistance(point, start)
	}
	fraction := ((point.X-start.X)*dx + (point.Y-start.Y)*dy) / denominator
	fraction = math.Max(0, math.Min(1, fraction))
	projection := fieldXY{X: start.X + fraction*dx, Y: start.Y + fraction*dy}
	return fieldXYDistance(point, projection)
}

func fieldXYDistance(left, right fieldXY) float64 {
	return math.Hypot(right.X-left.X, right.Y-left.Y)
}

func fieldEdgeKey(left, right int) [2]int {
	if left > right {
		left, right = right, left
	}
	return [2]int{left, right}
}

func fieldNoise(seed int64, stage, left, right int) float64 {
	key := fieldEdgeKey(left, right)
	value := uint64(seed) ^ uint64(stage+1)*0x9e3779b97f4a7c15 ^ uint64(key[0]+1)*0xbf58476d1ce4e5b9 ^ uint64(key[1]+1)*0x94d049bb133111eb
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	return float64(value>>11) / float64(uint64(1)<<53)
}

type fieldQueueItem struct {
	node     int
	distance float64
}

type fieldPriorityQueue []fieldQueueItem

func (q fieldPriorityQueue) Len() int           { return len(q) }
func (q fieldPriorityQueue) Less(i, j int) bool { return q[i].distance < q[j].distance }
func (q fieldPriorityQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *fieldPriorityQueue) Push(value any)    { *q = append(*q, value.(fieldQueueItem)) }
func (q *fieldPriorityQueue) Pop() any {
	old := *q
	item := old[len(old)-1]
	*q = old[:len(old)-1]
	return item
}
