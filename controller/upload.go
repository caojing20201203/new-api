package controller

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	uploadDir    = "uploads"
	maxImageSize = 5 * 1024 * 1024 // 5MB
)

var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/jpg":  true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

func init() {
	// 确保上传目录存在
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		fmt.Printf("Failed to create upload directory: %v\n", err)
	}
}

func UploadImage(c *gin.Context) {
	file, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "未获取到上传的图片",
		})
		return
	}

	// 检查文件大小
	if file.Size > maxImageSize {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "图片大小不能超过 5MB",
		})
		return
	}

	// 检查文件类型
	ext := strings.ToLower(filepath.Ext(file.Filename))
	contentType := file.Header.Get("Content-Type")
	if !allowedImageTypes[contentType] {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "不支持的图片类型，仅支持 JPG、PNG、GIF、WEBP",
		})
		return
	}

	// 生成唯一文件名
	now := time.Now()
	dateDir := now.Format("2006/01")
	fullDir := filepath.Join(uploadDir, dateDir)

	// 创建日期目录
	if err := os.MkdirAll(fullDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "创建上传目录失败",
		})
		return
	}

	// 生成唯一文件名
	fileName := fmt.Sprintf("%s%s", uuid.New().String(), ext)
	filePath := filepath.Join(fullDir, fileName)

	// 保存文件
	if err := c.SaveUploadedFile(file, filePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "保存图片失败",
		})
		return
	}

	// 返回可访问的 URL 路径
	urlPath := fmt.Sprintf("/%s/%s/%s", uploadDir, dateDir, fileName)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"url": urlPath,
		},
	})
}
