package main

import (
	"sort"
	"strings"
)

func urlize(str string) string {
	newStr := ""
	for _, c := range strings.Split(strings.ToLower(str), "") {
		if c >= "a" && c <= "z" || c >= "A" && c <= "Z" || c >= "0" && c <= "9" {
			newStr += c
		} else if c == " " {
			newStr += "-"
		}
	}
	return newStr
}

func firstSentences(value string, count int) string {
	for i := range value {
		if value[i] == '.' || value[i] == '!' || value[i] == '?' {
			count -= 1
			if count == 0 && i < len(value) {
				return value[0 : i+1]
			}
		}
	}
	return value
}

func sortedStrings(s []string) []string {
	sort.Slice(s, func(i, j int) bool {
		return strings.ToLower(s[i]) < strings.ToLower(s[j])
	})
	return s
}
