package dataset

import (
	"math"
	"time"

	"github.com/brianvoe/gofakeit/v7"
)

func Cents(c int) float64 { return float64(c) / 100 }

func Ptr[T any](v T) *T { return &v }

// Lebih banyak nilai di dekat 0 bila skew > 1.
func Skewed(f *gofakeit.Faker, skew float64) float64 {
	return math.Pow(f.Float64Range(0, 1), skew)
}

func Between(f *gofakeit.Faker, start, end time.Time) time.Time {
	if !end.After(start) {
		return start
	}
	return start.Add(time.Duration(f.Float64Range(0, 1) * float64(end.Sub(start))))
}

func Pick[T any](f *gofakeit.Faker, items []T) T {
	return items[f.IntRange(0, len(items)-1)]
}
