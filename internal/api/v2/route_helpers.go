package v2

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func registerRouteWithSlashVariants(rg *gin.RouterGroup, method, relativePath string, handlers ...gin.HandlerFunc) {
	rg.Handle(method, relativePath, handlers...)
	if strings.HasSuffix(relativePath, "/") {
		return
	}
	rg.Handle(method, relativePath+"/", handlers...)
}

func registerGetWithSlashVariants(rg *gin.RouterGroup, relativePath string, handlers ...gin.HandlerFunc) {
	registerRouteWithSlashVariants(rg, http.MethodGet, relativePath, handlers...)
}

func registerPostWithSlashVariants(rg *gin.RouterGroup, relativePath string, handlers ...gin.HandlerFunc) {
	registerRouteWithSlashVariants(rg, http.MethodPost, relativePath, handlers...)
}

func registerPutWithSlashVariants(rg *gin.RouterGroup, relativePath string, handlers ...gin.HandlerFunc) {
	registerRouteWithSlashVariants(rg, http.MethodPut, relativePath, handlers...)
}

func registerDeleteWithSlashVariants(rg *gin.RouterGroup, relativePath string, handlers ...gin.HandlerFunc) {
	registerRouteWithSlashVariants(rg, http.MethodDelete, relativePath, handlers...)
}
