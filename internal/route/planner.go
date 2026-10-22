package route

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/mapapi"
)

type PlanRequest struct {
	Start           geo.Point   `json:"start"`
	Via             []geo.Point `json:"via,omitempty"`
	TargetDistanceM float64     `json:"target_distance_m"`
	MaxDistanceM    float64     `json:"max_distance_m,omitempty"`
	// FairnessKey groups provider calls for global round-robin scheduling.
	// It is assigned by trusted callers and is never accepted from JSON.
	FairnessKey string `json:"-"`
	// HeadingDeg is retained only for compatibility with older API clients.
	// Road-network planning intentionally imposes no heading or shape.
	HeadingDeg     float64     `json:"heading_deg,omitempty"`
	CandidateCount int         `json:"candidate_count,omitempty"`
	Boundary       []geo.Point `json:"boundary,omitempty"`
	Seed           int64       `json:"seed,omitempty"`
}

type Quality struct {
	DistanceErrorPct float64 `json:"distance_error_pct"`
	RetraceRatio     float64 `json:"retrace_ratio"`
	OutsideAreaRatio float64 `json:"outside_area_ratio"`
	Score            float64 `json:"score"`
}

type Route struct {
	ID                 string      `json:"id"`
	Source             string      `json:"source"`
	CoordinateSystem   string      `json:"coordinate_system"`
	GeneratedAt        time.Time   `json:"generated_at"`
	TargetDistanceM    float64     `json:"target_distance_m"`
	DistanceM          float64     `json:"distance_m"`
	EstimatedDurationS float64     `json:"estimated_duration_s"`
	Points             []geo.Point `json:"points"`
	Quality            Quality     `json:"quality"`
}

type Planner struct {
	walker        mapapi.Walker
	maxCandidates int
}

func NewPlanner(walker mapapi.Walker, maxCandidates int) *Planner {
	if maxCandidates < 1 {
		maxCandidates = 4
	}
	return &Planner{walker: walker, maxCandidates: maxCandidates}
}

func (p *Planner) PlanRoute(ctx context.Context, request PlanRequest) (Route, error) {
	ctx = mapapi.WithFairnessKey(ctx, request.FairnessKey)
	if !request.Start.Valid() {
		return Route{}, errors.New("start coordinate is invalid")
	}
	if request.TargetDistanceM < 300 || request.TargetDistanceM > 100_000 {
		return Route{}, errors.New("target_distance_m must be between 300 and 100000")
	}
	// MaxDistanceM limits the selectable target, not the final recorded result.
	// The latter may legitimately finish up to 50 metres beyond the selected
	// target, including when the highest selectable target was chosen.
	if request.MaxDistanceM > 0 && request.TargetDistanceM > request.MaxDistanceM+0.01 {
		return Route{}, errors.New("target_distance_m exceeds the server maximum")
	}
	if len(request.Boundary) > 0 {
		if len(request.Boundary) < 3 {
			return Route{}, errors.New("boundary requires at least three points")
		}
		for _, point := range request.Boundary {
			if !point.Valid() {
				return Route{}, errors.New("boundary contains an invalid coordinate")
			}
		}
		adjustedStart, err := nearestInteriorStart(request.Start, request.Boundary)
		if err != nil {
			return Route{}, fmt.Errorf("place start inside boundary: %w", err)
		}
		request.Start = adjustedStart
	}
	for _, point := range request.Via {
		if !point.Valid() {
			return Route{}, errors.New("via contains an invalid coordinate")
		}
	}
	count := request.CandidateCount
	if count <= 0 || count > p.maxCandidates {
		count = p.maxCandidates
	}
	routeSeed := request.Seed
	if routeSeed == 0 {
		routeSeed = time.Now().UnixNano()
	}
	network, roadErr := p.discoverRoadPoints(ctx, request, count)
	fieldStart := request.Start
	canUseFieldFallback := len(request.Boundary) >= 3 && allPointsInside(request.Via, request.Boundary)
	if canUseFieldFallback &&
		(roadErr != nil || !roadNetworkSuitable(network, count)) {
		return planAutomaticFieldRoute(ctx, request, count, routeSeed)
	}
	if roadErr != nil {
		return Route{}, roadErr
	}
	roadPoints := network.Points
	roadStart, ok := nearestProviderRoadPoint(request.Start, roadPoints, request.Boundary)
	if !ok {
		if canUseFieldFallback {
			request.Start = fieldStart
			return planAutomaticFieldRoute(ctx, request, count, routeSeed)
		}
		return Route{}, errors.New("road-network discovery did not return a usable start point")
	}
	// The route must begin on provider geometry, not at the raw fence centre or
	// another arbitrary coordinate. All subsequent randomized waypoints are
	// provider vertices as well, so every walking leg starts and ends on-road.
	request.Start = roadStart

	type result struct {
		route Route
		err   error
	}
	results := make(chan result, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(candidate int) {
			defer wg.Done()
			route, err := p.planCandidate(ctx, request, roadPoints, routeSeed, candidate)
			results <- result{route: route, err: err}
		}(i)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var routes []Route
	var firstErr error
	for result := range results {
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		routes = append(routes, result.route)
	}
	if len(routes) == 0 {
		if canUseFieldFallback {
			request.Start = fieldStart
			return planAutomaticFieldRoute(ctx, request, count, routeSeed)
		}
		if firstErr == nil {
			firstErr = errors.New("no route candidate succeeded")
		}
		return Route{}, firstErr
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Quality.Score < routes[j].Quality.Score })
	return routes[0], nil
}

func (p *Planner) planCandidate(ctx context.Context, request PlanRequest, roadPoints []geo.Point, routeSeed int64, candidate int) (Route, error) {
	points := []geo.Point{request.Start}
	current := request.Start
	var duration float64
	for index, target := range request.Via {
		leg, err := p.walker.Walking(ctx, current, target)
		if err != nil {
			return Route{}, fmt.Errorf("candidate %d clock-point leg %d: %w", candidate+1, index+1, err)
		}
		if len(leg.Points) < 2 {
			return Route{}, fmt.Errorf("candidate %d clock-point leg %d returned no geometry", candidate+1, index+1)
		}
		points = append(points, leg.Points[1:]...)
		duration += leg.DurationS
		current = target
	}

	mandatoryDistance := geo.PolylineDistance(points)
	maximumDistance := request.TargetDistanceM + 50
	if mandatoryDistance > maximumDistance+0.05 {
		return Route{}, fmt.Errorf("clock-point route is %.1f m, beyond the allowed maximum %.1f m", mandatoryDistance, maximumDistance)
	}

	headroom := maximumDistance - request.TargetDistanceM
	overrun := math.Min(headroom, float64(8+(candidate*13)%35))
	desiredDistance := request.TargetDistanceM + overrun
	if mandatoryDistance > desiredDistance {
		desiredDistance = mandatoryDistance
	}
	if mandatoryDistance+0.001 < desiredDistance {
		var err error
		points, duration, err = p.extendRoute(ctx, request, roadPoints, routeSeed, candidate, points, current, duration, desiredDistance)
		if err != nil {
			return Route{}, err
		}
	}

	// Densify each original edge independently so every server checkpoint and
	// every provider road vertex remains an exact point in the final geometry.
	// Resampling the whole polyline at a global five-metre cadence would only
	// pass near a checkpoint (up to 2.5 m away) instead of through it.
	dense, err := densifyPreservingVertices(points, 5)
	if err != nil {
		return Route{}, fmt.Errorf("candidate %d geometry: %w", candidate+1, err)
	}
	beforeClip := geo.PolylineDistance(dense)
	dense, err = clipPolylineDistance(dense, desiredDistance)
	if err != nil {
		return Route{}, fmt.Errorf("candidate %d distance fitting (have %.3f m, need %.3f m): %w", candidate+1, beforeClip, desiredDistance, err)
	}
	fittedDistance := geo.PolylineDistance(dense)
	if beforeClip > 0 {
		duration *= fittedDistance / beforeClip
	}
	retrace := retraceRatio(dense, 12)
	outside := outsideRatio(dense, request.Boundary)
	errorPct := math.Abs(fittedDistance-request.TargetDistanceM) / request.TargetDistanceM * 100
	score := errorPct + retrace*35 + outside*220
	source := "map_api_road_network"
	if len(request.Via) > 0 {
		source = "map_api_road_network_via"
	}
	return Route{
		ID:                 newID("route"),
		Source:             source,
		CoordinateSystem:   "provider_native",
		GeneratedAt:        time.Now().UTC(),
		TargetDistanceM:    request.TargetDistanceM,
		DistanceM:          fittedDistance,
		EstimatedDurationS: duration,
		Points:             dense,
		Quality: Quality{
			DistanceErrorPct: round(errorPct, 3),
			RetraceRatio:     round(retrace, 4),
			OutsideAreaRatio: round(outside, 4),
			Score:            round(score, 3),
		},
	}, nil
}

// discoverRoadPoints builds a reusable road-point atlas before candidate
// planning. Probe coordinates are only search inputs: the points returned from
// this function are interior vertices taken from the map provider's walking
// polylines, so final waypoints are on the provider road network rather than on
// synthetic rays around the start.
type roadNetwork struct {
	Points      []geo.Point
	TotalCount  int
	InsideCount int
}

func (p *Planner) discoverRoadPoints(ctx context.Context, request PlanRequest, candidateCount int) (roadNetwork, error) {
	atlasRandom := rand.New(rand.NewSource(stableRoadSeed(request)))
	probeCount := candidateCount * 2
	if probeCount < 6 {
		probeCount = 6
	}
	if probeCount > 12 {
		probeCount = 12
	}

	all := make([]geo.Point, 0, 96)
	inside := make([]geo.Point, 0, 96)
	collect := func(leg mapapi.Leg) {
		for _, point := range providerRoadPoints(leg.Points, 18) {
			appendDistinctPoint(&all, point, 18)
			if len(request.Boundary) < 3 || pointInPolygon(point, request.Boundary) {
				appendDistinctPoint(&inside, point, 18)
			}
		}
	}

	// Mandatory legs are useful atlas material and will be served from cache
	// when each candidate later connects the same clock points.
	origins := []geo.Point{request.Start}
	current := request.Start
	var firstErr error
	for index, target := range request.Via {
		leg, err := p.walker.Walking(ctx, current, target)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("discover clock-point road leg %d: %w", index+1, err)
			}
		} else {
			collect(leg)
		}
		origins = append(origins, target)
		current = target
	}

	for index := 0; index < probeCount; index++ {
		probe, ok := randomProbePoint(atlasRandom, request)
		if !ok {
			continue
		}
		origin := origins[index%len(origins)]
		if geo.Distance(origin, probe) < 20 {
			continue
		}
		leg, err := p.walker.Walking(ctx, origin, probe)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("discover road-network probe %d: %w", index+1, err)
			}
			continue
		}
		collect(leg)
	}

	selected := all
	// Prefer a wholly in-area road atlas when the provider exposes enough road
	// vertices there. If it does not, retain provider road points outside the
	// polygon and let candidate outside-area scoring decide whether they are
	// usable instead of silently reverting to fabricated coordinates.
	if len(request.Boundary) >= 3 && len(inside) >= maxInt(4, candidateCount) {
		selected = inside
	}
	if len(selected) < 2 {
		if firstErr != nil {
			return roadNetwork{}, fmt.Errorf("road-network discovery produced too few usable points: %w", firstErr)
		}
		return roadNetwork{}, errors.New("road-network discovery produced too few usable points")
	}
	return roadNetwork{
		Points: evenlyLimitPoints(selected, 96), TotalCount: len(all), InsideCount: len(inside),
	}, nil
}

func roadNetworkSuitable(network roadNetwork, candidateCount int) bool {
	if len(network.Points) < 2 || network.TotalCount == 0 {
		return false
	}
	// Judge usable in-fence coverage by its absolute atlas, not by the ratio to
	// all provider points. A single probe may legitimately leave a gated campus
	// and contribute many outside vertices; using that ratio misclassified a
	// well-mapped campus such as 雁塔校区 as an unmapped athletics field.
	minimumInside := maxInt(6, candidateCount+2)
	return network.InsideCount >= minimumInside
}

func nearestProviderRoadPoint(start geo.Point, roadPoints, boundary []geo.Point) (geo.Point, bool) {
	best := geo.Point{}
	bestDistance := math.Inf(1)
	for _, point := range roadPoints {
		if !point.Valid() {
			continue
		}
		if len(boundary) >= 3 && !pointInPolygon(point, boundary) {
			continue
		}
		if distance := geo.Distance(start, point); distance < bestDistance {
			best, bestDistance = point, distance
		}
	}
	return best, best.Valid()
}

// nearestInteriorStart repairs server fence centres that sit just outside a
// concave polygon. It selects the closest point with a small inward clearance,
// leaving fully valid centres unchanged. Road-mode planning later snaps this
// value once more to the nearest provider polyline vertex.
func nearestInteriorStart(start geo.Point, boundary []geo.Point) (geo.Point, error) {
	if len(boundary) < 3 {
		return start, nil
	}
	projection := newFieldProjection(boundary[0])
	polygon := make([]fieldXY, 0, len(boundary))
	for _, point := range boundary {
		polygon = append(polygon, projection.toXY(point))
	}
	startXY := projection.toXY(start)
	if pointInPolygonXY(startXY, polygon) || distanceToPolygonXY(startXY, polygon) <= 0.25 {
		return start, nil
	}

	best := fieldXY{}
	bestDistance := math.Inf(1)
	consider := func(candidate fieldXY, clearanceM float64) {
		if !pointInPolygonXY(candidate, polygon) || distanceToPolygonXY(candidate, polygon) < clearanceM {
			return
		}
		if distance := fieldXYDistance(startXY, candidate); distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}

	// Probe inward around every closest boundary projection. At least one side
	// of a non-degenerate polygon edge is interior, including concave notches.
	for index := range polygon {
		left := polygon[index]
		right := polygon[(index+1)%len(polygon)]
		edgePoint := projectToSegmentXY(startXY, left, right)
		for _, radius := range []float64{1.5, 3, 6, 12, 24} {
			for bearing := 0; bearing < 360; bearing += 15 {
				angle := float64(bearing) * math.Pi / 180
				consider(fieldXY{X: edgePoint.X + math.Sin(angle)*radius, Y: edgePoint.Y + math.Cos(angle)*radius}, 0.75)
			}
		}
	}
	if !math.IsInf(bestDistance, 1) {
		return projection.toGeo(best), nil
	}

	// Degenerate or extremely narrow polygons get a deterministic grid search
	// fallback so route planning remains fully automatic.
	minX, maxX, minY, maxY := polygon[0].X, polygon[0].X, polygon[0].Y, polygon[0].Y
	for _, point := range polygon[1:] {
		minX, maxX = math.Min(minX, point.X), math.Max(maxX, point.X)
		minY, maxY = math.Min(minY, point.Y), math.Max(maxY, point.Y)
	}
	for y := 0; y <= 48; y++ {
		for x := 0; x <= 48; x++ {
			candidate := fieldXY{
				X: minX + (maxX-minX)*float64(x)/48,
				Y: minY + (maxY-minY)*float64(y)/48,
			}
			consider(candidate, 0.1)
		}
	}
	if math.IsInf(bestDistance, 1) {
		return geo.Point{}, errors.New("boundary contains no usable interior point")
	}
	return projection.toGeo(best), nil
}

func projectToSegmentXY(point, start, end fieldXY) fieldXY {
	dx, dy := end.X-start.X, end.Y-start.Y
	denominator := dx*dx + dy*dy
	if denominator <= 1e-12 {
		return start
	}
	fraction := ((point.X-start.X)*dx + (point.Y-start.Y)*dy) / denominator
	fraction = math.Max(0, math.Min(1, fraction))
	return fieldXY{X: start.X + fraction*dx, Y: start.Y + fraction*dy}
}

func allPointsInside(points, polygon []geo.Point) bool {
	if len(polygon) < 3 {
		return true
	}
	projection := newFieldProjection(polygon[0])
	projectedPolygon := make([]fieldXY, 0, len(polygon))
	for _, boundaryPoint := range polygon {
		projectedPolygon = append(projectedPolygon, projection.toXY(boundaryPoint))
	}
	for _, point := range points {
		if !insideOrBoundaryXY(projection.toXY(point), projectedPolygon, 2) {
			return false
		}
	}
	return true
}

// extendRoute performs a randomized greedy search over provider-derived road
// nodes. Every edge is then resolved again by the walking route API, while the
// required clock-point prefix has already been fixed by planCandidate.
func (p *Planner) extendRoute(
	ctx context.Context,
	request PlanRequest,
	roadPoints []geo.Point,
	routeSeed int64,
	candidate int,
	points []geo.Point,
	current geo.Point,
	duration, desiredDistance float64,
) ([]geo.Point, float64, error) {
	if len(roadPoints) < 2 {
		return nil, 0, errors.New("road network contains fewer than two usable points")
	}
	anchors := append([]geo.Point(nil), request.Via...)
	anchors = append([]geo.Point{request.Start}, anchors...)
	routeRandom := rand.New(rand.NewSource(routeSeed + int64(candidate+1)*104729))
	uses := make([]int, len(roadPoints))
	lastRoadPoint := -1
	// Densifying provider polylines replaces many tiny coordinate segments and
	// can shorten the measured geodesic length by a few metres. Build a small
	// geometry reserve, then clip the dense line to the exact selected result.
	geometryTarget := desiredDistance + math.Max(10, desiredDistance*0.005)

	for legIndex := 0; geo.PolylineDistance(points) < geometryTarget; legIndex++ {
		if legIndex >= 32 {
			return nil, 0, fmt.Errorf("candidate %d route could not reach the selected distance after 32 legs", candidate+1)
		}
		remaining := geometryTarget - geo.PolylineDistance(points)
		roadIndex, ok := chooseRoadPoint(routeRandom, current, request.Boundary, roadPoints, anchors, uses, lastRoadPoint, remaining, legIndex)
		if !ok {
			return nil, 0, fmt.Errorf("candidate %d: road-network search found no reachable next waypoint", candidate+1)
		}
		destination := roadPoints[roadIndex]
		leg, err := p.walker.Walking(ctx, current, destination)
		if err != nil {
			return nil, 0, fmt.Errorf("candidate %d road leg %d: %w", candidate+1, legIndex+1, err)
		}
		if len(leg.Points) < 2 || geo.PolylineDistance(leg.Points) < 1 {
			return nil, 0, fmt.Errorf("candidate %d road leg %d returned no geometry", candidate+1, legIndex+1)
		}
		points = append(points, leg.Points[1:]...)
		duration += leg.DurationS
		current = destination
		anchors = append(anchors, destination)
		uses[roadIndex]++
		lastRoadPoint = roadIndex
	}
	return points, duration, nil
}

func chooseRoadPoint(
	random *rand.Rand,
	current geo.Point,
	boundary, roadPoints, anchors []geo.Point,
	uses []int,
	lastRoadPoint int,
	remaining float64,
	legIndex int,
) (int, bool) {
	nominalLeg := math.Min(1200, math.Max(80, remaining/2.5))
	if remaining < nominalLeg {
		nominalLeg = math.Max(50, remaining*1.05)
	}
	maximumUses := 1 + legIndex/maxInt(1, len(roadPoints))
	bestIndex, bestScore := -1, math.Inf(1)
	for index, point := range roadPoints {
		if index == lastRoadPoint || uses[index] > maximumUses {
			continue
		}
		distance := geo.Distance(current, point)
		if distance < 20 {
			continue
		}
		novelty := nominalLeg
		for _, anchor := range anchors {
			novelty = math.Min(novelty, geo.Distance(point, anchor))
		}
		outsidePenalty := 0.0
		if len(boundary) >= 3 && !pointInPolygon(point, boundary) {
			outsidePenalty = 350
		}
		reusePenalty := float64(uses[index]) * nominalLeg * 0.7
		jitter := (random.Float64() - 0.5) * nominalLeg * 0.35
		score := math.Abs(distance-nominalLeg) - math.Min(novelty, nominalLeg)*0.28 + outsidePenalty + reusePenalty + jitter
		if score < bestScore {
			bestIndex, bestScore = index, score
		}
	}
	return bestIndex, bestIndex >= 0
}

func providerRoadPoints(points []geo.Point, spacingM float64) []geo.Point {
	if len(points) < 3 {
		return nil
	}
	// Walking() prepends the requested origin and appends the requested
	// destination. Excluding both keeps only vertices decoded from provider
	// step polylines.
	provider := points[1 : len(points)-1]
	result := make([]geo.Point, 0, len(provider))
	for _, point := range provider {
		if len(result) == 0 || geo.Distance(result[len(result)-1], point) >= spacingM {
			result = append(result, point)
		}
	}
	return result
}

func densifyPreservingVertices(points []geo.Point, spacingM float64) ([]geo.Point, error) {
	if len(points) < 2 {
		return nil, errors.New("route requires at least two points")
	}
	result := []geo.Point{points[0]}
	for index := 1; index < len(points); index++ {
		distance := geo.Distance(points[index-1], points[index])
		if distance <= 0.05 {
			continue
		}
		if distance < 1 {
			result = append(result, points[index])
			continue
		}
		segment, err := geo.Densify([]geo.Point{points[index-1], points[index]}, spacingM)
		if err != nil {
			return nil, err
		}
		result = append(result, segment[1:]...)
	}
	return geo.Deduplicate(result, 0.01), nil
}

func randomProbePoint(random *rand.Rand, request PlanRequest) (geo.Point, bool) {
	if len(request.Boundary) >= 3 {
		minLat, maxLat := request.Boundary[0].Lat, request.Boundary[0].Lat
		minLng, maxLng := request.Boundary[0].Lng, request.Boundary[0].Lng
		for _, point := range request.Boundary[1:] {
			minLat, maxLat = math.Min(minLat, point.Lat), math.Max(maxLat, point.Lat)
			minLng, maxLng = math.Min(minLng, point.Lng), math.Max(maxLng, point.Lng)
		}
		for attempt := 0; attempt < 128; attempt++ {
			point := geo.Point{
				Lat: minLat + random.Float64()*(maxLat-minLat),
				Lng: minLng + random.Float64()*(maxLng-minLng),
			}
			if pointInPolygon(point, request.Boundary) && geo.Distance(point, request.Start) >= 20 {
				return point, true
			}
		}
		return geo.Point{}, false
	}
	radius := math.Min(2000, math.Max(250, request.TargetDistanceM/2.5))
	distance := radius * (0.3 + 0.7*math.Sqrt(random.Float64()))
	return geo.Destination(request.Start, random.Float64()*360, distance), true
}

func appendDistinctPoint(points *[]geo.Point, candidate geo.Point, minimumDistanceM float64) {
	if !candidate.Valid() {
		return
	}
	for _, point := range *points {
		if geo.Distance(point, candidate) < minimumDistanceM {
			return
		}
	}
	*points = append(*points, candidate)
}

func evenlyLimitPoints(points []geo.Point, maximum int) []geo.Point {
	if maximum <= 0 || len(points) <= maximum {
		return append([]geo.Point(nil), points...)
	}
	result := make([]geo.Point, 0, maximum)
	for index := 0; index < maximum; index++ {
		position := int(math.Floor(float64(index) * float64(len(points)) / float64(maximum)))
		result = append(result, points[position])
	}
	return result
}

func stableRoadSeed(request PlanRequest) int64 {
	hash := fnv.New64a()
	_, _ = fmt.Fprintf(hash, "%.6f,%.6f", request.Start.Lat, request.Start.Lng)
	for _, point := range request.Boundary {
		_, _ = fmt.Fprintf(hash, "|%.6f,%.6f", point.Lat, point.Lng)
	}
	return int64(hash.Sum64())
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func clipPolylineDistance(points []geo.Point, desiredM float64) ([]geo.Point, error) {
	line, err := geo.NewPolyline(points)
	if err != nil {
		return nil, err
	}
	if desiredM <= 0 || line.LengthM+0.01 < desiredM {
		return nil, errors.New("route geometry is shorter than desired distance")
	}
	result := make([]geo.Point, 0, len(line.Points))
	for index, distance := range line.Cumulative {
		if distance >= desiredM-0.001 {
			break
		}
		result = append(result, line.Points[index])
	}
	end, _ := line.At(desiredM)
	result = append(result, end)
	return geo.Deduplicate(result, 0.01), nil
}

func outsideRatio(points, polygon []geo.Point) float64 {
	if len(polygon) < 3 || len(points) == 0 {
		return 0
	}
	projection := newFieldProjection(polygon[0])
	projectedPolygon := make([]fieldXY, 0, len(polygon))
	for _, boundaryPoint := range polygon {
		projectedPolygon = append(projectedPolygon, projection.toXY(boundaryPoint))
	}
	outside := 0
	for _, point := range points {
		if !insideOrBoundaryXY(projection.toXY(point), projectedPolygon, 2) {
			outside++
		}
	}
	return float64(outside) / float64(len(points))
}

func pointInPolygon(point geo.Point, polygon []geo.Point) bool {
	inside := false
	for i, j := 0, len(polygon)-1; i < len(polygon); j, i = i, i+1 {
		a, b := polygon[i], polygon[j]
		intersects := (a.Lat > point.Lat) != (b.Lat > point.Lat) &&
			point.Lng < (b.Lng-a.Lng)*(point.Lat-a.Lat)/(b.Lat-a.Lat)+a.Lng
		if intersects {
			inside = !inside
		}
	}
	return inside
}

func retraceRatio(points []geo.Point, cellM float64) float64 {
	if len(points) < 3 {
		return 0
	}
	lat0 := points[0].Lat * math.Pi / 180
	seen := make(map[[2]int]int, len(points))
	duplicates := 0
	for _, point := range points {
		x := point.Lng * math.Pi / 180 * 6371008.8 * math.Cos(lat0)
		y := point.Lat * math.Pi / 180 * 6371008.8
		cell := [2]int{int(math.Round(x / cellM)), int(math.Round(y / cellM))}
		if seen[cell] > 0 {
			duplicates++
		}
		seen[cell]++
	}
	return float64(duplicates) / float64(len(points))
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
}

func round(v float64, digits int) float64 {
	power := math.Pow10(digits)
	return math.Round(v*power) / power
}
