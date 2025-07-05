// Copyright (C) 2025 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"errors"
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
	return &PostAssert{pr: pr, params: make([]PostParam, 0)}
}

func (pa *PostAssert) AddParameter(name string, typ PostFieldType, required bool) {
	pa.params = append(pa.params, PostParam{Name: name, Type: typ, Required: required})
}

func (pa *PostAssert) Assert() ([]error, bool) {
	errs := make([]error, 0)
	for _, p := range pa.params {
		val := strings.TrimSpace(pa.pr.PostFormValue(p.Name))

		// Check presence
		if val == "" {
			if p.Required {
				errs = append(errs, errors.New("parameter '"+p.Name+"' is required"))
			}
			continue
		}

		switch p.Type {
		case STRING:
			// always valid
		case INTEGER:
			if _, err := strconv.Atoi(val); err != nil {
				errs = append(errs, errors.New("parameter '"+p.Name+"': expected integer"))
			}
		case FLOAT:
			if _, err := strconv.ParseFloat(val, 64); err != nil {
				errs = append(errs, errors.New("parameter '"+p.Name+"': expected float"))
			}
		case POSITIVE_INTEGER:
			if i, err := strconv.Atoi(val); err != nil || i <= 0 {
				errs = append(errs, errors.New("parameter '"+p.Name+"': expected positive integer"))
			}
		case POSITIVE_FLOAT:
			if f, err := strconv.ParseFloat(val, 64); err != nil || f <= 0 {
				errs = append(errs, errors.New("parameter '"+p.Name+"': expected positive float"))
			}
		case PERC_FLOAT:
			if f, err := strconv.ParseFloat(val, 64); err != nil || f < 0 || f > 1 {
				errs = append(errs, errors.New("parameter '"+p.Name+"': expected percentage between 0 and 1"))
			}
		case DATE:
			if _, err := time.Parse("2006-01-02", val); err != nil {
				errs = append(errs, errors.New("parameter '"+p.Name+"': expected date in yyyy-mm-dd format"))
			}
		case TIME:
			if _, err := time.Parse("15:04:05", val); err != nil {
				errs = append(errs, errors.New("parameter '"+p.Name+"': expected time in hh:mm:ss format"))
			}
		case DATETIME:
			if _, err := time.Parse("2006-01-02 15:04:05", val); err != nil {
				errs = append(errs, errors.New("parameter '"+p.Name+"': expected datetime in yyyy-mm-dd hh:mm:ss format"))
			}
		}
	}
	return errs, len(errs) == 0
}
