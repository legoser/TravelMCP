// Package main — standalone-коннектор формата реестра Минтранса РФ.
// Переводит срез реестра (reestr.json) в универсальный flat-формат
// flat_trips.json; конвейер sync работает только с flat-форматом и
// ничего не знает о специфике источника.
package main

import (
	"regexp"
)

var (
	reestrRegRe   = regexp.MustCompile(`^\d{2}\.\d{2}\.\d+(?:/\d+)?$`)
	reestrTimeRe  = regexp.MustCompile(`^\d{1,2}:\d{2}$`)
	reestrDwellRe = regexp.MustCompile(`^\d{1,3}(:\d{2})?$`)
	reestrDateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reestrCodeRe  = regexp.MustCompile(`^\d{2}$`)
)
