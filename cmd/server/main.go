package main

import (
	"log-api/configs"
	"log-api/internal/routes"
)

//TIP To run your code, right-click the code and select <b>Run</b>. Alternatively, click
// the <icon src="AllIcons.Actions.Execute"/> icon in the gutter and select the <b>Run</b> menu item from here.

func main() {
	//TIP Press <shortcut actionId="ShowIntentionActions"/> when your caret is at the underlined or highlighted text
	// to see how GoLand suggests fixing it.
	cfg := configs.GetConfig()

	//privateLogger := pkg.SetupLogger(cfg.PrivateLogFile)
	//log.Println("[LOG] %v", privateLogger.Level)
	//if privateLogger == nil {
	//	log.Fatalf("privateLogger is nil")
	//}

	//publicLogger := pkg.SetupLogger(cfg.PublicLogFile)
	//if publicLogger == nil {
	//	log.Fatalf("publicLogger is nil")
	//}

	routes.SetupRoutes(cfg)
}

//TIP See GoLand help at <a href="https://www.jetbrains.com/help/go/">jetbrains.com/help/go/</a>.
// Also, you can try interactive lessons for GoLand by selecting 'Help | Learn IDE Features' from the main menu.
