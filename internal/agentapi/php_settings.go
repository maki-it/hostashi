// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// PHPSettings are the PHP ini settings of one site (ADR-0049): a closed
// allowlist with typed values. The pool renders them as php_value, so they
// are defaults the site's .user.ini and ini_set() may still change; the
// hard limits are the slice (ADR-0015) and nginx, which follows
// PostMaxSize and MaxExecutionTime.
//
// The zero value of every field means "Hostashi default"
// (DefaultPHPSettings).
type PHPSettings struct {
	MemoryLimit          uint64 `json:"memoryLimit,omitempty"`          // bytes
	UploadMaxFilesize    uint64 `json:"uploadMaxFilesize,omitempty"`    // bytes
	PostMaxSize          uint64 `json:"postMaxSize,omitempty"`          // bytes
	MaxExecutionTime     int    `json:"maxExecutionTime,omitempty"`     // seconds
	MaxInputTime         int    `json:"maxInputTime,omitempty"`         // seconds
	MaxInputVars         int    `json:"maxInputVars,omitempty"`         //
	MaxFileUploads       int    `json:"maxFileUploads,omitempty"`       //
	Timezone             string `json:"timezone,omitempty"`             // IANA name
	DisplayErrors        *bool  `json:"displayErrors,omitempty"`        //
	ErrorReporting       string `json:"errorReporting,omitempty"`       // one of the ErrorReporting presets
	SessionGCMaxLifetime int    `json:"sessionGcMaxlifetime,omitempty"` // seconds
	ShortOpenTag         *bool  `json:"shortOpenTag,omitempty"`         //
}

// The presets of error_reporting (ADR-0049).
const (
	ErrorReportingProduction  = "production"
	ErrorReportingDevelopment = "development"
	ErrorReportingErrors      = "errors"
)

// errorReportingValues are the ini expressions of the presets.
var errorReportingValues = map[string]string{
	ErrorReportingProduction:  "E_ALL & ~E_DEPRECATED",
	ErrorReportingDevelopment: "E_ALL",
	ErrorReportingErrors:      "E_ERROR | E_PARSE | E_CORE_ERROR | E_COMPILE_ERROR",
}

const (
	mib = 1 << 20
	gib = 1 << 30
)

// Bounds of the PHP settings (ADR-0049).
const (
	MinPHPMemoryLimit          uint64 = 32 * mib
	MaxPHPMemoryLimit          uint64 = 4 * gib
	MinPHPUploadSize           uint64 = 1 * mib
	MaxPHPUploadSize           uint64 = 2 * gib
	MinPHPTime                        = 10
	MaxPHPTime                        = 3600
	MinPHPInputVars                   = 1000
	MaxPHPInputVars                   = 100000
	MinPHPFileUploads                 = 1
	MaxPHPFileUploads                 = 500
	MinPHPSessionLifetime             = 60
	MaxPHPSessionLifetime             = 30 * 24 * 3600
	maxTimezoneLen                    = 64
	minPHPFastCGIReadTimeout          = 60
	phpFastCGIReadTimeoutGrace        = 10
)

// DefaultPHPSettings are the values of a site that sets none (ADR-0049).
func DefaultPHPSettings() PHPSettings {
	f := false

	return PHPSettings{
		MemoryLimit:          256 * mib,
		UploadMaxFilesize:    64 * mib,
		PostMaxSize:          64 * mib,
		MaxExecutionTime:     60,
		MaxInputTime:         60,
		MaxInputVars:         3000,
		MaxFileUploads:       20,
		Timezone:             "UTC",
		DisplayErrors:        &f,
		ErrorReporting:       ErrorReportingProduction,
		SessionGCMaxLifetime: 1440,
		ShortOpenTag:         &f,
	}
}

// ErrPHPSetting marks a rejected PHP setting.
var ErrPHPSetting = errors.New("php setting rejected")

// PHPSettingError names the rejected setting by its ini key.
type PHPSettingError struct {
	Key    string
	Reason string
}

func (e *PHPSettingError) Error() string { return "php setting " + e.Key + ": " + e.Reason }

// Unwrap makes errors.Is(err, ErrPHPSetting) true.
func (e *PHPSettingError) Unwrap() error { return ErrPHPSetting }

func reject(key, format string, args ...any) error {
	return &PHPSettingError{Key: key, Reason: fmt.Sprintf(format, args...)}
}

// timezoneRe allows "UTC" and Area/Location names; it keeps dots and
// absolute paths away from the zone database lookup.
var timezoneRe = regexp.MustCompile(`^[A-Z][A-Za-z_+-]*(/[A-Z0-9][A-Za-z0-9_+-]*){0,2}$`)

// ValidTimezone reports whether name is an IANA time zone the system
// knows. "Local" is not one.
func ValidTimezone(name string) bool {
	if len(name) > maxTimezoneLen || !timezoneRe.MatchString(name) || name == "Local" {
		return false
	}

	_, err := time.LoadLocation(name)

	return err == nil
}

func checkRange[T int | uint64](key string, v, lo, hi T) error {
	if v != 0 && (v < lo || v > hi) {
		return reject(key, "must be between %d and %d", lo, hi)
	}

	return nil
}

// Validate checks every set field against its bounds, and that
// post_max_size is at least upload_max_filesize after defaults.
func (s PHPSettings) Validate() error {
	checks := []error{
		checkRange("memory_limit", s.MemoryLimit, MinPHPMemoryLimit, MaxPHPMemoryLimit),
		checkRange("upload_max_filesize", s.UploadMaxFilesize, MinPHPUploadSize, MaxPHPUploadSize),
		checkRange("post_max_size", s.PostMaxSize, MinPHPUploadSize, MaxPHPUploadSize),
		checkRange("max_execution_time", s.MaxExecutionTime, MinPHPTime, MaxPHPTime),
		checkRange("max_input_time", s.MaxInputTime, MinPHPTime, MaxPHPTime),
		checkRange("max_input_vars", s.MaxInputVars, MinPHPInputVars, MaxPHPInputVars),
		checkRange("max_file_uploads", s.MaxFileUploads, MinPHPFileUploads, MaxPHPFileUploads),
		checkRange("session.gc_maxlifetime", s.SessionGCMaxLifetime, MinPHPSessionLifetime, MaxPHPSessionLifetime),
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}

	if s.MemoryLimit%mib != 0 || s.UploadMaxFilesize%mib != 0 || s.PostMaxSize%mib != 0 {
		return reject("size", "sizes must be whole MiB")
	}

	if s.Timezone != "" && !ValidTimezone(s.Timezone) {
		return reject("date.timezone", "unknown time zone")
	}

	if _, ok := errorReportingValues[s.ErrorReporting]; s.ErrorReporting != "" && !ok {
		return reject("error_reporting", "must be production, development or errors")
	}

	e := s.Effective()
	if e.PostMaxSize < e.UploadMaxFilesize {
		return reject("post_max_size", "must be at least upload_max_filesize")
	}

	return nil
}

// Effective fills every unset field with its default.
func (s PHPSettings) Effective() PHPSettings {
	d := DefaultPHPSettings()

	pick := func(v, def int) int {
		if v == 0 {
			return def
		}

		return v
	}
	pickSize := func(v, def uint64) uint64 {
		if v == 0 {
			return def
		}

		return v
	}

	e := PHPSettings{
		MemoryLimit:          pickSize(s.MemoryLimit, d.MemoryLimit),
		UploadMaxFilesize:    pickSize(s.UploadMaxFilesize, d.UploadMaxFilesize),
		PostMaxSize:          pickSize(s.PostMaxSize, d.PostMaxSize),
		MaxExecutionTime:     pick(s.MaxExecutionTime, d.MaxExecutionTime),
		MaxInputTime:         pick(s.MaxInputTime, d.MaxInputTime),
		MaxInputVars:         pick(s.MaxInputVars, d.MaxInputVars),
		MaxFileUploads:       pick(s.MaxFileUploads, d.MaxFileUploads),
		Timezone:             s.Timezone,
		DisplayErrors:        s.DisplayErrors,
		ErrorReporting:       s.ErrorReporting,
		SessionGCMaxLifetime: pick(s.SessionGCMaxLifetime, d.SessionGCMaxLifetime),
		ShortOpenTag:         s.ShortOpenTag,
	}

	// Without its own post_max_size a larger upload size raises it, so
	// setting only upload_max_filesize works.
	if s.PostMaxSize == 0 {
		e.PostMaxSize = max(e.PostMaxSize, e.UploadMaxFilesize)
	}

	if e.Timezone == "" {
		e.Timezone = d.Timezone
	}

	if e.DisplayErrors == nil {
		e.DisplayErrors = d.DisplayErrors
	}

	if e.ErrorReporting == "" {
		e.ErrorReporting = d.ErrorReporting
	}

	if e.ShortOpenTag == nil {
		e.ShortOpenTag = d.ShortOpenTag
	}

	return e
}

// PHPIniValue is one php_value line of a pool.
type PHPIniValue struct {
	Key   string
	Value string
}

func onOff(b bool) string {
	if b {
		return "On"
	}

	return "Off"
}

func megabytes(b uint64) string { return strconv.FormatUint(b/mib, 10) + "M" }

// IniValues returns the effective settings as ini keys and values, in a
// fixed order. Callers validate first; the values contain only digits,
// letters, the zone name and the fixed error_reporting expressions.
func (s PHPSettings) IniValues() []PHPIniValue {
	e := s.Effective()

	return []PHPIniValue{
		{"memory_limit", megabytes(e.MemoryLimit)},
		{"upload_max_filesize", megabytes(e.UploadMaxFilesize)},
		{"post_max_size", megabytes(e.PostMaxSize)},
		{"max_execution_time", strconv.Itoa(e.MaxExecutionTime)},
		{"max_input_time", strconv.Itoa(e.MaxInputTime)},
		{"max_input_vars", strconv.Itoa(e.MaxInputVars)},
		{"max_file_uploads", strconv.Itoa(e.MaxFileUploads)},
		{"date.timezone", e.Timezone},
		{"display_errors", onOff(*e.DisplayErrors)},
		{"error_reporting", errorReportingValues[e.ErrorReporting]},
		{"session.gc_maxlifetime", strconv.Itoa(e.SessionGCMaxLifetime)},
		{"short_open_tag", onOff(*e.ShortOpenTag)},
	}
}

// ClientMaxBodySize is the site's nginx client_max_body_size in bytes:
// post_max_size, so a larger body never reaches PHP (ADR-0049).
func (s PHPSettings) ClientMaxBodySize() uint64 { return s.Effective().PostMaxSize }

// FastCGIReadTimeout is the site's nginx fastcgi_read_timeout in seconds:
// max_execution_time plus a grace period, at least nginx's 60 s default.
func (s PHPSettings) FastCGIReadTimeout() int {
	return max(s.Effective().MaxExecutionTime+phpFastCGIReadTimeoutGrace, minPHPFastCGIReadTimeout)
}
