package handlers

import (
	"github.com/gin-gonic/gin"
	"log-api/internal/mappers"
	"log-api/internal/services"
	"log-api/internal/utils"
	"net/http"
)

var LogService services.LogService

// @Summary Request public log
// @Description Request public log
// @Tags Log
// @Accept json
// @Produce json
// @Param log body mappers.LogRequest true "Log request"
// @Success 200 {object} utils.ApiError
// @Failure 409 {object} utils.ApiError
// @Router /public/log [post]
func HandlePublicLog(c *gin.Context) {
	var request mappers.LogRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		utils.SendApiError(c, utils.ErrorInvalidRequest)
		return
	}
	LogService.PublicLog(request)
	c.JSON(http.StatusOK, gin.H{"message": http.StatusOK, "code": nil, "data": nil})
}

// @Summary Request public log
// @Description Request public log
// @Tags Log
// @Accept json
// @Produce json
// @Param log body mappers.LogRequest true "Log request"
// @Param x-api-key header string true "API Key"
// @Param x-secret-key header string true "Secret Key"
// @Success 200 {object} utils.ApiError
// @Failure 409 {object} utils.ApiError
// @Router /private/log [post]
func HandlePrivateLog(c *gin.Context) {
	apiKey := c.GetHeader("x-api-key")
	if apiKey == "" {
		utils.SendApiError(c, utils.ErrorInvalidHeader)
	}

	secretKey := c.GetHeader("x-secret-key")
	if secretKey == "" {
		utils.SendApiError(c, utils.ErrorInvalidHeader)
	}

	var request mappers.LogRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		utils.SendApiError(c, utils.ErrorInvalidRequest)
		return
	}
	err := LogService.PrivateLog(request, apiKey, secretKey)
	if err != nil {
		utils.SendApiError(c, utils.ErrorInvalidRequest)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": http.StatusOK, "code": nil, "data": nil})
}
