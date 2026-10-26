package route

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Zolohacker/EasyRun/internal/geo"
)

// ValidateManual checks every vertex before deduplication (which would otherwise
// hide NaNs), and checks segments as well as vertices against the area fence.
func ValidateManual(points, boundary []geo.Point, minimumM float64) ([]geo.Point, float64, error) {
	if len(points) < 2 || len(points) > 10000 {
		return nil, 0, errors.New("路线需要 2–10000 个坐标点")
	}
	for _, p := range points {
		if !p.Valid() {
			return nil, 0, errors.New("路线包含无效坐标")
		}
	}
	line, err := geo.NewPolyline(points)
	if err != nil {
		return nil, 0, err
	}
	if math.IsNaN(minimumM) || math.IsInf(minimumM, 0) || minimumM < 300 {
		minimumM = 300
	}
	if line.LengthM <= minimumM || line.LengthM > 100000 {
		return nil, 0, fmt.Errorf("手绘路线须大于 %.0f 米，且不超过 100 km（当前 %.1f 米）", minimumM, line.LengthM)
	}
	if len(boundary) > 0 {
		if len(boundary) < 3 {
			return nil, 0, errors.New("区域边界不完整，请重新同步区域")
		}
		for _, p := range boundary {
			if !p.Valid() {
				return nil, 0, errors.New("区域边界坐标无效")
			}
		}
		dense, err := geo.Densify(line.Points, 3)
		if err != nil {
			return nil, 0, err
		}
		if !allPointsInside(line.Points, boundary) || !allPointsInside(dense, boundary) {
			return nil, 0, errors.New("路线经过区域边界之外，请调整折点")
		}
	}
	return line.Points, line.LengthM, nil
}

// ManualPrefix never extends, repeats, closes, or snaps user geometry. Preserve
// bends and interpolate only the last point to obtain the selected distance.
func ManualPrefix(id string, points []geo.Point, distanceM float64) (Route, error) {
	if math.IsNaN(distanceM) || math.IsInf(distanceM, 0) || distanceM < 300 || distanceM > 100000 {
		return Route{}, errors.New("手绘路线截取距离不正确")
	}
	for _, p := range points {
		if !p.Valid() {
			return Route{}, errors.New("路线包含无效坐标")
		}
	}
	if distanceM > geo.PolylineDistance(points)+1e-6 {
		return Route{}, errors.New("任务距离大于手绘路线长度")
	}
	clipped, err := clipPolylineDistance(points, distanceM)
	if err != nil {
		return Route{}, err
	}
	return Route{ID: id, Source: "manual", CoordinateSystem: "GCJ-02", GeneratedAt: time.Now().UTC(), TargetDistanceM: distanceM, DistanceM: geo.PolylineDistance(clipped), Points: clipped}, nil
}

// CoversInOrder only validates required waypoints. It does not synthesize a
// visit or alter the drawing when a checkpoint is missing.
func CoversInOrder(points, waypoints []geo.Point, toleranceM float64) bool {
	if len(waypoints) == 0 {
		return true
	}
	dense, err := geo.Densify(points, 2)
	if err != nil {
		return false
	}
	next := 0
	for _, p := range dense {
		if geo.Distance(p, waypoints[next]) <= toleranceM {
			next++
			if next == len(waypoints) {
				return true
			}
		}
	}
	return false
}
