package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ScheduleParser determines the next time a schedule expression should fire.
type ScheduleParser interface {
	Next(after time.Time) (time.Time, error)
}

// ParseSchedule parses a schedule expression. Supported forms:
//   - "@every <duration>" (e.g. "@every 6h", "@every 30m")
//   - a 5-field cron expression: "minute hour day-of-month month day-of-week"
//     with exact values, "*" (any), and "*/n" (step) support.
func ParseSchedule(expr string) (ScheduleParser, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("scheduler: empty schedule expression")
	}

	if strings.HasPrefix(expr, "@every ") {
		d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(expr, "@every")))
		if err != nil {
			return nil, fmt.Errorf("scheduler: invalid interval %q: %w", expr, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("scheduler: interval must be positive")
		}
		return &intervalSchedule{interval: d}, nil
	}

	return parseCron(expr)
}

type intervalSchedule struct {
	interval time.Duration
}

func (s *intervalSchedule) Next(after time.Time) (time.Time, error) {
	return after.Add(s.interval), nil
}

type cronSchedule struct {
	minute  fieldSet
	hour    fieldSet
	dom     fieldSet
	month   fieldSet
	dow     fieldSet
}

func parseCron(expr string) (*cronSchedule, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("scheduler: cron expression must have 5 fields, got %d", len(parts))
	}
	cs := &cronSchedule{}
	var err error
	if cs.minute, err = parseField(parts[0], 0, 59); err != nil {
		return nil, fmt.Errorf("scheduler: minute field: %w", err)
	}
	if cs.hour, err = parseField(parts[1], 0, 23); err != nil {
		return nil, fmt.Errorf("scheduler: hour field: %w", err)
	}
	if cs.dom, err = parseField(parts[2], 1, 31); err != nil {
		return nil, fmt.Errorf("scheduler: day-of-month field: %w", err)
	}
	if cs.month, err = parseField(parts[3], 1, 12); err != nil {
		return nil, fmt.Errorf("scheduler: month field: %w", err)
	}
	if cs.dow, err = parseField(parts[4], 0, 6); err != nil {
		return nil, fmt.Errorf("scheduler: day-of-week field: %w", err)
	}
	return cs, nil
}

func (c *cronSchedule) Next(after time.Time) (time.Time, error) {
	t := after.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ { // search up to ~1 year
		if c.matches(t) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("scheduler: could not find next cron firing time")
}

func (c *cronSchedule) matches(t time.Time) bool {
	if !c.minute.contains(t.Minute()) {
		return false
	}
	if !c.hour.contains(t.Hour()) {
		return false
	}
	if !c.dom.contains(t.Day()) {
		return false
	}
	if !c.month.contains(int(t.Month())) {
		return false
	}
	if !c.dow.contains(int(t.Weekday())) {
		return false
	}
	return true
}

type fieldSet struct {
	values map[int]struct{}
}

func parseField(s string, min, max int) (fieldSet, error) {
	fs := fieldSet{values: make(map[int]struct{})}
	if s == "*" {
		for i := min; i <= max; i++ {
			fs.values[i] = struct{}{}
		}
		return fs, nil
	}
	if strings.HasPrefix(s, "*/") {
		step, err := strconv.Atoi(strings.TrimPrefix(s, "*/"))
		if err != nil || step <= 0 {
			return fs, fmt.Errorf("invalid step %q", s)
		}
		for i := min; i <= max; i += step {
			fs.values[i] = struct{}{}
		}
		return fs, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fs, fmt.Errorf("invalid field value %q", s)
	}
	if n < min || n > max {
		return fs, fmt.Errorf("value %d out of range [%d,%d]", n, min, max)
	}
	fs.values[n] = struct{}{}
	return fs, nil
}

func (f fieldSet) contains(v int) bool {
	_, ok := f.values[v]
	return ok
}
