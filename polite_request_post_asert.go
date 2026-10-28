// Copyright (C) 2025-2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PostFieldType defines supported POST parameter data types for validation.
type PostFieldType int

const (
	STRING PostFieldType = iota
	INTEGER
	FLOAT
	POSITIVE_INTEGER
	POSITIVE_FLOAT
	PERC_FLOAT // float 0 <= x <= 1
	DATE       // yyyy-mm-dd
	TIME       // hh:mm:ss
	DATETIME   // yyyy-mm-dd hh:mm:ss
)

// check is how a PostFieldType validates a value: valid accepts it, want names
// what was expected in the error otherwise.
type check struct {
	valid func(string) bool
	want  string
}

func isInt(ok func(int) bool) func(string) bool {
	return func(v string) bool { i, err := strconv.Atoi(v); return err == nil && ok(i) }
}

func isFloat(ok func(float64) bool) func(string) bool {
	return func(v string) bool { f, err := strconv.ParseFloat(v, 64); return err == nil && ok(f) }
}

func isTime(layout string) func(string) bool {
	return func(v string) bool { _, err := time.Parse(layout, v); return err == nil }
}

var checks = map[PostFieldType]check{
	STRING:           {func(string) bool { return true }, ""},
	INTEGER:          {isInt(func(int) bool { return true }), "integer"},
	FLOAT:            {isFloat(func(float64) bool { return true }), "float"},
	POSITIVE_INTEGER: {isInt(func(i int) bool { return i > 0 }), "positive integer"},
	POSITIVE_FLOAT:   {isFloat(func(f float64) bool { return f > 0 }), "positive float"},
	PERC_FLOAT:       {isFloat(func(f float64) bool { return f >= 0 && f <= 1 }), "percentage between 0 and 1"},
	DATE:             {isTime("2006-01-02"), "date in yyyy-mm-dd format"},
	TIME:             {isTime("15:04:05"), "time in hh:mm:ss format"},
	DATETIME:         {isTime("2006-01-02 15:04:05"), "datetime in yyyy-mm-dd hh:mm:ss format"},
}

type PostParam struct {
	Name     string        // parameter name
	Type     PostFieldType // expected data type
	Required bool          // whether the parameter is mandatory
}

type PostAssert struct {
	pr     PoliteRequest
	params []PostParam
}

func InitPoliteRequestPostInterface(pr PoliteRequest) *PostAssert {
	return &PostAssert{pr: pr}
}

func (pa *PostAssert) AddParameter(name string, typ PostFieldType, required bool) {
	pa.params = append(pa.params, PostParam{Name: name, Type: typ, Required: required})
}

// Assert validates every parameter added against the form, returning the
// errors found and whether there were none.
func (pa *PostAssert) Assert() ([]error, bool) {
	errs := make([]error, 0)

	for _, p := range pa.params {
		val := strings.TrimSpace(pa.pr.PostFormValue(p.Name))

		if val == "" {
			if p.Required {
				errs = append(errs, fmt.Errorf("parameter '%s' is required", p.Name))
			}
		} else if c, known := checks[p.Type]; known && !c.valid(val) {
			errs = append(errs, fmt.Errorf("parameter '%s': expected %s", p.Name, c.want))
		}
	}

	return errs, len(errs) == 0
}
