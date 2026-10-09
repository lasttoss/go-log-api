package utils

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

type ErrorCode int

const (
	ErrorInvalidRequest ErrorCode = iota + 1000
	ErrorInvalidHeader
)

var errorMessages = map[ErrorCode]string{
	ErrorInvalidRequest: "Invalid request",
	ErrorInvalidHeader:  "Invalid header",
}

type ApiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *ApiError) Error() string {
	return e.Message
}

func SendApiError(c *gin.Context, code ErrorCode) {
	c.JSON(http.StatusConflict, gin.H{"message": errorMessages[code], "code": code, "data": nil})
}
