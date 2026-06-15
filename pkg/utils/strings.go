package utils

import (
	"math/rand/v2"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func RandString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.IntN(len(letterRunes))]
	}
	return string(b)
}

func Unique(arr []string) []string {
	unique := make(map[string]bool)
	var result []string
	for _, item := range arr {
		if _, ok := unique[item]; !ok {
			unique[item] = true
			result = append(result, item)
		}
	}
	return result
}
