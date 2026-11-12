package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const imageResultCleanupTimezone = "Asia/Shanghai"

func defaultImageResultCleanupPolicy(days int) ImageResultCleanupPolicy {
	if days <= 0 {
		days = 90
	}
	return ImageResultCleanupPolicy{Enabled: true, RetentionValue: days, RetentionUnit: "days", ScheduleMode: "interval", IntervalMinutes: 15, DailyTime: "03:00", Timezone: imageResultCleanupTimezone, BatchSize: 50, RequestsPerSecond: 2}
}

func imageResultCleanupAge(value int, unit string) (time.Duration, error) {
	max, multiplier := 87600, time.Hour
	if unit == "days" {
		max, multiplier = 3650, 24*time.Hour
	} else if unit != "hours" {
		return 0, fmt.Errorf("时间单位必须是 hours 或 days")
	}
	if value < 1 || value > max {
		return 0, fmt.Errorf("保留时间必须为 1～%d %s", max, unit)
	}
	return time.Duration(value) * multiplier, nil
}

func validateImageResultCleanupPolicy(p *ImageResultCleanupPolicy) error {
	if _, err := imageResultCleanupAge(p.RetentionValue, p.RetentionUnit); err != nil {
		return err
	}
	if p.ScheduleMode != "interval" && p.ScheduleMode != "daily" {
		return fmt.Errorf("请选择固定间隔或每日定点")
	}
	if p.IntervalMinutes < 1 || p.IntervalMinutes > 10080 {
		return fmt.Errorf("执行间隔必须为 1～10080 分钟")
	}
	if _, err := time.Parse("15:04", p.DailyTime); err != nil {
		return fmt.Errorf("每日时间必须为 HH:MM")
	}
	if p.Timezone != imageResultCleanupTimezone {
		return fmt.Errorf("清理调度时区必须为 Asia/Shanghai")
	}
	if p.BatchSize < 1 || p.BatchSize > 100 {
		return fmt.Errorf("单批数量必须为 1～100")
	}
	if p.RequestsPerSecond < 1 || p.RequestsPerSecond > 5 {
		return fmt.Errorf("存储请求速度必须为每秒 1～5 次")
	}
	return nil
}

func nextImageResultCleanupRun(p ImageResultCleanupPolicy, now time.Time) time.Time {
	if p.ScheduleMode != "daily" {
		return now.UTC().Add(time.Duration(p.IntervalMinutes) * time.Minute)
	}
	clock, _ := time.Parse("15:04", p.DailyTime)
	// Daily schedules intentionally use Beijing time, independent of host TZ.
	zone := time.FixedZone(imageResultCleanupTimezone, 8*60*60)
	local := now.In(zone)
	next := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, zone)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC()
}

type imageResultCleanupFilters struct {
	Before         *time.Time `json:"before"`
	OlderThanValue int        `json:"older_than_value"`
	OlderThanUnit  string     `json:"older_than_unit"`
	PreviewToken   string     `json:"preview_token"`
}

func parseImageResultCleanupFilters(raw json.RawMessage, now time.Time) (imageResultCleanupFilters, error) {
	var f imageResultCleanupFilters
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &f); err != nil {
			return f, fmt.Errorf("清理条件格式错误")
		}
	}
	if f.Before != nil && (f.OlderThanValue != 0 || f.OlderThanUnit != "") {
		return f, fmt.Errorf("截止时间和相对时间不能同时填写")
	}
	if f.Before == nil {
		age, err := imageResultCleanupAge(f.OlderThanValue, f.OlderThanUnit)
		if err != nil {
			return f, err
		}
		before := now.UTC().Add(-age)
		f.Before = &before
	}
	if f.Before.IsZero() || !f.Before.Before(now) {
		return f, fmt.Errorf("清理截止时间必须早于当前时间")
	}
	before := f.Before.UTC()
	f.Before = &before
	f.PreviewToken = strings.TrimSpace(f.PreviewToken)
	return f, nil
}
