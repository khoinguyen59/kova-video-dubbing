package handler

import "kova/internal/service"

type Handler struct {
	Service *service.Service
}

func NewHandler() *Handler {
	return &Handler{
		Service: service.NewService(),
	}
}
