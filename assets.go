package main

import (
	rice "github.com/GeertJohan/go.rice"
)

var backendAssets *rice.Box

func init() {
	var err error
	backendAssets, err = rice.FindBox("backend-assets")
	if err != nil {
		panic(err)
	}
}
