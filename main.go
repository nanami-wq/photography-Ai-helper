package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-service-starter/config"
	"go-service-starter/core/gin"

	"github.com/spf13/viper"
)

func main() {
	handler := gin.GinInit()
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%s", config.GetConfig().App.Port),
		Handler: handler,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Server Error: %s", err.Error())
		}
	}()
	log.Printf("Server Run At: http://localhost:%s", viper.GetString("app.port"))
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutdown Server ...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server Shutdown Error: %s", err.Error())
	} else {
		log.Println("Server Shutdown Gracefully")
	}
}
