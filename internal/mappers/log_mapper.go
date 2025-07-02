package mappers

type LogRequest struct {
	UserId   string `json:"user_id" binding:"required"`
	Key      string `json:"key" binding:"required"`
	Data     string `json:"data" binding:"required"`
	Metadata string `json:"metadata" binding:"required"`
}
