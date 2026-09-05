package controller

import (
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/resource"
	"myproject/internal/service"
	"myproject/pkg/errcode"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// Register 用户注册
// @Summary 用户注册
// @Tags 用户
// @Accept json
// @Produce json
// @Param request body model.UserRegisterRequest true "注册信息"
// @Success 200 {object} response.Response{data=model.UserResponse}
// @Router /api/v1/users/register [post]
func Register(c *gin.Context) {
	var req model.UserRegisterRequest
	if !bindJSON(c, &req) {
		return
	}

	user, err := service.Register(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, user)
}

// Login 用户登录
// @Summary 用户登录
// @Tags 用户
// @Accept json
// @Produce json
// @Param request body model.UserLoginRequest true "登录信息"
// @Success 200 {object} response.Response{data=model.LoginResponse}
// @Router /api/v1/users/login [post]
func Login(c *gin.Context) {
	var req model.UserLoginRequest
	if !bindJSON(c, &req) {
		return
	}

	user, err := service.Login(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}

	jwt := resource.JWT()
	token, err := jwt.GenerateToken(user.ID, user.Username)
	if err != nil {
		response.Error(c, errcode.ErrTokenGenerate.WithCause(err))
		return
	}

	response.Success(c, model.LoginResponse{
		Token:     token,
		ExpiresIn: int64(jwt.ExpireDuration().Seconds()),
		User:      user.ToResponse(),
	})
}

// GetProfile 获取当前用户信息
// @Summary 获取当前用户信息
// @Tags 用户
// @Produce json
// @Security Bearer
// @Success 200 {object} response.Response{data=model.UserResponse}
// @Router /api/v1/users/profile [get]
func GetProfile(c *gin.Context) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	user, err := service.GetUser(c.Request.Context(), userID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, user)
}

// UpdateProfile 更新当前用户信息
// @Summary 更新当前用户信息
// @Tags 用户
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body model.UserUpdateRequest true "更新信息"
// @Success 200 {object} response.Response{data=model.UserResponse}
// @Router /api/v1/users/profile [put]
func UpdateProfile(c *gin.Context) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req model.UserUpdateRequest
	if !bindJSON(c, &req) {
		return
	}

	user, err := service.UpdateUser(c.Request.Context(), userID, &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, user)
}

// GetUser 获取指定用户的公开信息
// @Summary 获取指定用户的公开信息
// @Tags 用户
// @Produce json
// @Security Bearer
// @Param id path int true "用户ID"
// @Success 200 {object} response.Response{data=model.UserPublicResponse}
// @Router /api/v1/users/{id} [get]
func GetUser(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}

	// 本人查自己走 /users/profile，这里一律按「他人视角」返回，不含 email/phone
	user, err := service.GetUserPublic(c.Request.Context(), id)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, user)
}

// ListUsers 获取用户列表（公开信息）
// @Summary 获取用户列表
// @Tags 用户
// @Produce json
// @Security Bearer
// @Param page query int false "页码，最大 10000"
// @Param page_size query int false "每页数量，最大 100"
// @Success 200 {object} response.Response{data=response.ListData}
// @Router /api/v1/users [get]
func ListUsers(c *gin.Context) {
	var req model.PageRequest
	if !bindQuery(c, &req) {
		return
	}

	users, total, err := service.ListUsers(c.Request.Context(), req.Page, req.PageSize)
	if err != nil {
		response.Error(c, err)
		return
	}

	page, pageSize := req.Normalize()
	response.SuccessList(c, users, total, page, pageSize)
}

// DeleteUser 删除用户
// @Summary 删除用户
// @Tags 用户
// @Produce json
// @Security Bearer
// @Param id path int true "用户ID"
// @Success 200 {object} response.Response
// @Router /api/v1/users/{id} [delete]
func DeleteUser(c *gin.Context) {
	id, ok := pathID(c, "id")
	if !ok {
		return
	}

	if err := service.DeleteUser(c.Request.Context(), id); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, nil)
}
