package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminUserAPIKeyHandler lets the admin panel manage API keys on behalf of a user
// (user self-service routes were removed; this is the only key CRUD surface).
type AdminUserAPIKeyHandler struct {
	apiKeyService *service.APIKeyService
}

// NewAdminUserAPIKeyHandler creates the admin user API key handler
func NewAdminUserAPIKeyHandler(apiKeyService *service.APIKeyService) *AdminUserAPIKeyHandler {
	return &AdminUserAPIKeyHandler{apiKeyService: apiKeyService}
}

// CreateUserAPIKey creates an API key owned by the target user.
// POST /api/v1/admin/users/:id/api-keys
func (h *AdminUserAPIKeyHandler) CreateUserAPIKey(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var req service.CreateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	apiKey, err := h.apiKeyService.Create(c.Request.Context(), userID, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.APIKeyFromService(apiKey))
}

// UpdateUserAPIKey updates an API key owned by the target user.
// PUT /api/v1/admin/users/:id/api-keys/:keyId
func (h *AdminUserAPIKeyHandler) UpdateUserAPIKey(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	keyID, err := strconv.ParseInt(c.Param("keyId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}

	var req service.UpdateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	apiKey, err := h.apiKeyService.Update(c.Request.Context(), keyID, userID, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.APIKeyFromService(apiKey))
}

// DeleteUserAPIKey deletes an API key owned by the target user.
// DELETE /api/v1/admin/users/:id/api-keys/:keyId
func (h *AdminUserAPIKeyHandler) DeleteUserAPIKey(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	keyID, err := strconv.ParseInt(c.Param("keyId"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}

	if err := h.apiKeyService.Delete(c.Request.Context(), keyID, userID); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"deleted": true})
}
