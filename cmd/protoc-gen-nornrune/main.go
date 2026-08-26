package main

import (
	frontproto "github.com/sebishogun/nornrune/frontend/proto"
	"google.golang.org/protobuf/compiler/protogen"
)

func main() {
	protogen.Options{}.Run(frontproto.GeneratePlugin)
}
