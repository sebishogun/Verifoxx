package main

import (
	frontproto "github.com/sebishogun/verifoxx/frontend/proto"
	"google.golang.org/protobuf/compiler/protogen"
)

func main() {
	protogen.Options{}.Run(frontproto.GeneratePlugin)
}
