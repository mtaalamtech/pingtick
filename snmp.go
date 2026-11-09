package main

import (
	"math"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

// snmpValueMissing reports a reply saying the device has no value for the OID.
func snmpValueMissing(pdu gosnmp.SnmpPDU) bool {
	switch pdu.Type {
	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView, gosnmp.Null:
		return true
	}
	return false
}

// snmpNumber converts a reply to a number. Negative values wrap, and are
// restored by the int64 conversion when they are stored.
func snmpNumber(pdu gosnmp.SnmpPDU) (uint64, bool) {
	switch v := pdu.Value.(type) {
	case uint:
		return uint64(v), true
	case uint32:
		return uint64(v), true
	case uint64:
		return v, true
	case int:
		return uint64(v), true
	case int32:
		return uint64(v), true
	case int64:
		return uint64(v), true
	case float32:
		return uint64(int64(v)), true
	case float64:
		return uint64(int64(v)), true
	case []byte: // how the library returns an SNMP OctetString
		return parseSNMPText(string(v))
	case string:
		return parseSNMPText(v)
	}
	return 0, false
}

func parseSNMPText(text string) (uint64, bool) {
	text = strings.TrimSpace(text)
	if number, err := strconv.ParseUint(text, 10, 64); err == nil {
		return number, true
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return uint64(int64(number)), true
}
