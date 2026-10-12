package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Profile is one saved calibration; the JSON format matches the Python version.
type Profile struct {
	PPM     float64 `json:"ppm"`
	W       int     `json:"w"`
	H       int     `json:"h"`
	KnownMM float64 `json:"known_mm"`
	LinePx  float64 `json:"line_px"`
	Date    string  `json:"date"`
}

type calFile struct {
	Active   *string            `json:"active"`
	Profiles map[string]Profile `json:"profiles"`
}

func appDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

var calOverride string

func calPath() string {
	if calOverride != "" {
		return calOverride
	}
	return filepath.Join(appDir(), "calibrations.json")
}

func loadCalibrations() (map[string]Profile, string) {
	data, err := os.ReadFile(calPath())
	if err != nil {
		return map[string]Profile{}, ""
	}
	var f calFile
	if json.Unmarshal(data, &f) != nil || f.Profiles == nil {
		return map[string]Profile{}, ""
	}
	active := ""
	if f.Active != nil {
		if _, ok := f.Profiles[*f.Active]; ok {
			active = *f.Active
		}
	}
	return f.Profiles, active
}

func saveCalibrations(p map[string]Profile, active string) error {
	f := calFile{Profiles: p}
	if active != "" {
		f.Active = &active
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(calPath(), data, 0o644)
}

func profileNames(p map[string]Profile) []string {
	names := make([]string, 0, len(p))
	for n := range p {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
