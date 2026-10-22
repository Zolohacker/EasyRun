package geo

import (
	"errors"
	"math"
)

const earthRadiusM = 6371008.8

type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

func (p Point) Valid() bool {
	return !math.IsNaN(p.Lat) && !math.IsInf(p.Lat, 0) &&
		!math.IsNaN(p.Lng) && !math.IsInf(p.Lng, 0) &&
		p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}

func Distance(a, b Point) float64 {
	lat1 := radians(a.Lat)
	lat2 := radians(b.Lat)
	dLat := lat2 - lat1
	dLng := radians(b.Lng - a.Lng)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(math.Min(1, h)))
}

func PolylineDistance(points []Point) float64 {
	var total float64
	for i := 1; i < len(points); i++ {
		total += Distance(points[i-1], points[i])
	}
	return total
}

func Bearing(a, b Point) float64 {
	lat1 := radians(a.Lat)
	lat2 := radians(b.Lat)
	dLng := radians(b.Lng - a.Lng)
	y := math.Sin(dLng) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLng)
	return normalizeDegrees(degrees(math.Atan2(y, x)))
}

func Destination(start Point, bearingDeg, distanceM float64) Point {
	bearing := radians(bearingDeg)
	angular := distanceM / earthRadiusM
	lat1 := radians(start.Lat)
	lng1 := radians(start.Lng)
	lat2 := math.Asin(math.Sin(lat1)*math.Cos(angular) + math.Cos(lat1)*math.Sin(angular)*math.Cos(bearing))
	lng2 := lng1 + math.Atan2(math.Sin(bearing)*math.Sin(angular)*math.Cos(lat1), math.Cos(angular)-math.Sin(lat1)*math.Sin(lat2))
	return Point{Lat: degrees(lat2), Lng: normalizeLongitude(degrees(lng2))}
}

func OffsetMeters(p Point, eastM, northM float64) Point {
	lat := p.Lat + degrees(northM/earthRadiusM)
	cosLat := math.Cos(radians(p.Lat))
	if math.Abs(cosLat) < 1e-9 {
		cosLat = 1e-9
	}
	lng := p.Lng + degrees(eastM/(earthRadiusM*cosLat))
	return Point{Lat: lat, Lng: normalizeLongitude(lng)}
}

type Polyline struct {
	Points     []Point
	Cumulative []float64
	LengthM    float64
}

func NewPolyline(points []Point) (Polyline, error) {
	clean := Deduplicate(points, 0.05)
	if len(clean) < 2 {
		return Polyline{}, errors.New("route requires at least two distinct points")
	}
	for _, point := range clean {
		if !point.Valid() {
			return Polyline{}, errors.New("route contains an invalid coordinate")
		}
	}
	cumulative := make([]float64, len(clean))
	for i := 1; i < len(clean); i++ {
		cumulative[i] = cumulative[i-1] + Distance(clean[i-1], clean[i])
	}
	if cumulative[len(cumulative)-1] < 1 {
		return Polyline{}, errors.New("route is shorter than one metre")
	}
	return Polyline{Points: clean, Cumulative: cumulative, LengthM: cumulative[len(cumulative)-1]}, nil
}

func (p Polyline) At(distanceM float64) (Point, float64) {
	if distanceM <= 0 {
		return p.Points[0], Bearing(p.Points[0], p.Points[1])
	}
	if distanceM >= p.LengthM {
		n := len(p.Points)
		return p.Points[n-1], Bearing(p.Points[n-2], p.Points[n-1])
	}
	lo, hi := 0, len(p.Cumulative)-1
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if p.Cumulative[mid] <= distanceM {
			lo = mid
		} else {
			hi = mid
		}
	}
	segment := p.Cumulative[hi] - p.Cumulative[lo]
	if segment <= 0 {
		return p.Points[lo], Bearing(p.Points[lo], p.Points[hi])
	}
	fraction := (distanceM - p.Cumulative[lo]) / segment
	return interpolate(p.Points[lo], p.Points[hi], fraction), Bearing(p.Points[lo], p.Points[hi])
}

func (p Polyline) TurnSeverity(distanceM, lookM float64) float64 {
	if lookM <= 0 {
		lookM = 12
	}
	_, b1 := p.At(math.Max(0, distanceM-lookM))
	_, b2 := p.At(math.Min(p.LengthM, distanceM+lookM))
	delta := math.Abs(b1 - b2)
	if delta > 180 {
		delta = 360 - delta
	}
	return math.Min(1, delta/120)
}

func Densify(points []Point, spacingM float64) ([]Point, error) {
	line, err := NewPolyline(points)
	if err != nil {
		return nil, err
	}
	if spacingM <= 0 {
		spacingM = 5
	}
	result := make([]Point, 0, int(line.LengthM/spacingM)+2)
	for d := 0.0; d < line.LengthM; d += spacingM {
		point, _ := line.At(d)
		result = append(result, point)
	}
	result = append(result, line.Points[len(line.Points)-1])
	return Deduplicate(result, 0.05), nil
}

func Deduplicate(points []Point, thresholdM float64) []Point {
	if len(points) == 0 {
		return nil
	}
	result := []Point{points[0]}
	for _, point := range points[1:] {
		if Distance(result[len(result)-1], point) > thresholdM {
			result = append(result, point)
		}
	}
	return result
}

func interpolate(a, b Point, t float64) Point {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return Point{Lat: a.Lat + (b.Lat-a.Lat)*t, Lng: a.Lng + (b.Lng-a.Lng)*t}
}

func radians(v float64) float64 { return v * math.Pi / 180 }
func degrees(v float64) float64 { return v * 180 / math.Pi }
func normalizeDegrees(v float64) float64 {
	for v < 0 {
		v += 360
	}
	for v >= 360 {
		v -= 360
	}
	return v
}
func normalizeLongitude(v float64) float64 {
	for v < -180 {
		v += 360
	}
	for v > 180 {
		v -= 360
	}
	return v
}
