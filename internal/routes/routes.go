package routes

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"log"
	"log-api/configs"
	_ "log-api/docs"
	"log-api/internal/handlers"
	"log-api/internal/services"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func SetupRoutes(cfg configs.Config) {
	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	logService := services.NewLogService(cfg)
	handlers.LogService = logService

	r.POST("/private/log", handlers.HandlePrivateLog)
	r.POST("/public/log", handlers.HandlePublicLog)

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	srv := &http.Server{
		Addr:    cfg.ServerPort,
		Handler: r,
	}

	// Server run context
	go func() {
		log.Println("Starting server on %s", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")
}
