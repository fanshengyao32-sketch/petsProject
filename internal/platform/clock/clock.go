package clock

import "time"

type Clock interface {
	Now() time.Time
}

type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

type Fixed struct{ Time time.Time }

func (f Fixed) Now() time.Time { return f.Time.UTC() }

func BusinessDate(value time.Time, location *time.Location) string {
	return value.In(location).Format(time.DateOnly)
}
