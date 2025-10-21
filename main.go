package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shogo82148/androidbinary/apk"
	"github.com/skip2/go-qrcode"
)

// BuildInfo represents a specific app build version
type BuildInfo struct {
	Version      string `json:"version"`
	Channel      string `json:"channel"`
	ReleaseNotes string `json:"releaseNotes"`
	FileName     string `json:"fileName"`
	FileSize     int64  `json:"fileSize"`
	UploadTime   string `json:"uploadTime"`
	DownloadURL  string `json:"downloadURL"`
}

// AppEntry represents a unique app (identified by package name)
type AppEntry struct {
	AppName     string      `json:"appName"`
	PackageName string      `json:"packageName"`
	IconPath    string      `json:"iconPath"`
	Builds      []BuildInfo `json:"builds"`
}

// Project represents a project category
type Project struct {
	ProjectName string     `json:"projectName"`
	Apps        []AppEntry `json:"apps"`
}

const deletePassword = "9527"

var (
	allProjects      []Project
	mutex            = &sync.Mutex{}
	metadataFilePath = "metadata.json"
)

// loadMetadata loads the metadata from the JSON file.
// It locks the mutex to ensure thread safety.
func loadMetadata() error {
	mutex.Lock()
	defer mutex.Unlock()
	data, err := os.ReadFile(metadataFilePath) // Use os.ReadFile instead of ioutil.ReadFile
	if err != nil {
		if os.IsNotExist(err) {
			allProjects = []Project{}
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &allProjects)
}

// saveMetadata saves the metadata to the JSON file with a backup mechanism.
// IMPORTANT: It does NOT lock the mutex, assuming the caller has already acquired a lock.
func saveMetadata() error {
	// Create a backup before writing
	backupPath := metadataFilePath + ".bak"
	if _, err := os.Stat(metadataFilePath); err == nil {
		if err := os.Rename(metadataFilePath, backupPath); err != nil {
			return fmt.Errorf("创建元数据备份失败: %w", err)
		}
	}

	data, err := json.MarshalIndent(allProjects, "", "  ")
	if err != nil {
		// Attempt to restore from backup on marshaling error
		os.Rename(backupPath, metadataFilePath)
		return err
	}

	err = os.WriteFile(metadataFilePath, data, 0644) // Use os.WriteFile instead of ioutil.WriteFile
	if err != nil {
		// Attempt to restore from backup on write error
		os.Rename(backupPath, metadataFilePath)
		return fmt.Errorf("写入元数据文件失败: %w", err)
	}

	// If successful, remove the backup
	os.Remove(backupPath)
	return nil
}

func main() {
	if err := loadMetadata(); err != nil {
		panic("加载元数据失败: " + err.Error())
	}

	router := gin.Default()

	// Register custom template functions
	router.SetFuncMap(template.FuncMap{
		"formatSize": formatSize,
		"first":      first,
	})

	router.LoadHTMLGlob("templates/*")
	router.Static("/static", "./static")
	router.Static("/downloads", "./uploads")

	// Homepage route
	router.GET("/", func(c *gin.Context) {
		mutex.Lock() // Add mutex lock for thread-safe read
		defer mutex.Unlock()
		c.HTML(http.StatusOK, "index.html", gin.H{
			"AllProjects":  allProjects,
			"UploadStatus": c.Query("upload"),
		})
	})

	// App Detail Page Route
	router.GET("/app/:packageName", handleAppDetailPage)

	// Upload page
	router.GET("/upload", func(c *gin.Context) {
		c.HTML(http.StatusOK, "upload.html", nil)
	})

	// QR Code generator
	router.GET("/qr", func(c *gin.Context) {
		urlToEncode := c.Query("url")
		if urlToEncode == "" {
			c.String(http.StatusBadRequest, "URL 参数缺失")
			return
		}
		qr, err := qrcode.New(urlToEncode, qrcode.Medium)
		if err != nil {
			c.String(http.StatusInternalServerError, "无法生成二维码")
			return
		}
		c.Writer.Header().Set("Content-Type", "image/png")
		qr.Write(256, c.Writer)
	})

	// --- API Routes ---
	api := router.Group("/api")
	{
		api.POST("/upload", handleApiUpload)
		// NEW: Delete routes
		api.DELETE("/apps/:packageName", handleDeleteApp)
		api.DELETE("/builds/:packageName/:fileName", handleDeleteBuild)
	}

	fmt.Println("服务器已启动，监听端口:1234")
	router.Run(":1234")
}

// Handler for the App Detail Page
func handleAppDetailPage(c *gin.Context) {
	packageName := c.Param("packageName")
	var foundApp *AppEntry
	var projectOwner *Project

	mutex.Lock() // Add mutex lock for thread-safe read
	defer mutex.Unlock()

	// Find the correct app across all projects
	for i := range allProjects {
		for j := range allProjects[i].Apps {
			if allProjects[i].Apps[j].PackageName == packageName {
				foundApp = &allProjects[i].Apps[j]
				projectOwner = &allProjects[i]
				break
			}
		}
		if foundApp != nil {
			break
		}
	}

	if foundApp == nil {
		c.String(http.StatusNotFound, "应用未找到")
		return
	}

	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s", scheme, c.Request.Host)

	c.HTML(http.StatusOK, "details.html", gin.H{
		"App":         foundApp,
		"ProjectName": projectOwner.ProjectName,
		"BaseURL":     baseURL,
	})
}

// AppInfo holds information extracted from an APK
type AppInfo struct {
	AppName     string
	PackageName string
	Version     string
	IconPath    string
}

// --- API Handlers ---

func handleApiUpload(c *gin.Context) {
	fmt.Println("--- 收到新的上传请求 ---")

	projectName := c.PostForm("projectName")
	channel := c.PostForm("channel")
	releaseNotes := c.PostForm("releaseNotes")
	fmt.Printf("表单数据解析: 项目=%s, 渠道=%s\n", projectName, channel)

	file, err := c.FormFile("file")
	if err != nil {
		c.String(http.StatusBadRequest, "获取表单文件错误: %s", err.Error())
		return
	}
	fmt.Printf("文件已接收: %s, 大小: %d\n", file.Filename, file.Size)

	tempSavePath := filepath.Join("uploads", fmt.Sprintf("temp-%d-%s", time.Now().UnixNano(), filepath.Base(file.Filename)))
	if err := c.SaveUploadedFile(file, tempSavePath); err != nil {
		fmt.Printf("保存临时文件到 %s 错误: %v\n", tempSavePath, err)
		c.String(http.StatusInternalServerError, "保存文件错误: %s", err.Error())
		return
	}
	fmt.Printf("文件成功临时保存到: %s\n", tempSavePath)
	defer os.Remove(tempSavePath)

	pkg, err := apk.OpenFile(tempSavePath)
	if err != nil {
		c.String(http.StatusInternalServerError, "解析APK失败: %s", err.Error())
		return
	}
	defer pkg.Close()

	appName, err := pkg.Label(nil)
	if err != nil || appName == "" {
		c.String(http.StatusInternalServerError, "解析APK应用名失败或应用名为空: %v", err)
		return
	}
	packageName := pkg.PackageName()
	if packageName == "" {
		c.String(http.StatusInternalServerError, "解析APK包名失败或包名为空")
		return
	}
	version, err := pkg.Manifest().VersionName.String()
	if err != nil || version == "" {
		c.String(http.StatusInternalServerError, "解析APK版本名失败或版本名为空: %v", err)
		return
	}

	uniqueFilename := fmt.Sprintf("%s-%s-%s-%d.apk", packageName, version, channel, time.Now().Unix())
	finalSavePath := filepath.Join("uploads", uniqueFilename)

	tempFileBytes, err := os.ReadFile(tempSavePath)
	if err != nil {
		c.String(http.StatusInternalServerError, "无法读取临时文件: %s", err.Error())
		return
	}
	if err := os.WriteFile(finalSavePath, tempFileBytes, 0644); err != nil {
		c.String(http.StatusInternalServerError, "无法保存最终文件: %s", err.Error())
		return
	}
	fmt.Printf("文件已保存为: %s\n", finalSavePath)

	icon, err := pkg.Icon(nil)
	var iconPath string
	if err != nil {
		fmt.Printf("警告: 无法提取应用 '%s' 的图标: %v\n", appName, err)
		iconPath = ""
	} else {
		iconDir := filepath.Join("static", "icons")
		if err := os.MkdirAll(iconDir, 0755); err != nil {
			c.String(http.StatusInternalServerError, "无法创建图标目录: %s", err.Error())
			return
		}
		relativeIconPath := filepath.Join("static", "icons", fmt.Sprintf("%s.png", packageName))
		fullIconPath := relativeIconPath
		iconFile, err := os.Create(fullIconPath)
		if err != nil {
			c.String(http.StatusInternalServerError, "无法创建图标文件: %s", err.Error())
			return
		}
		defer iconFile.Close()
		if err := png.Encode(iconFile, icon); err != nil {
			c.String(http.StatusInternalServerError, "无法编码图标为PNG: %s", err.Error())
			return
		}
		iconPath = filepath.ToSlash(relativeIconPath)
		fmt.Printf("应用图标已保存到: %s\n", fullIconPath)
	}

	appInfo := AppInfo{AppName: appName, PackageName: packageName, Version: version, IconPath: iconPath}
	buildInfo := BuildInfo{
		Version:      appInfo.Version,
		Channel:      channel,
		ReleaseNotes: releaseNotes,
		FileName:     uniqueFilename,
		FileSize:     file.Size,
		UploadTime:   time.Now().Format("2006-01-02 15:04:05"),
		DownloadURL:  fmt.Sprintf("/downloads/%s", uniqueFilename),
	}

	if err := updateMetadata(projectName, appInfo, buildInfo); err != nil {
		fmt.Printf("更新元数据错误: %v\n", err)
		os.Remove(finalSavePath)
		c.String(http.StatusInternalServerError, "更新元数据失败: %s", err.Error())
		return
	}

	source := c.PostForm("source")
	if source == "web" {
		c.Redirect(http.StatusFound, "/?upload=success")
	} else {
		c.JSON(http.StatusOK, gin.H{"message": "Upload successful"})
	}
}

func handleDeleteBuild(c *gin.Context) {
	if c.Query("password") != deletePassword {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "删除密码错误"})
		return
	}

	packageName := c.Param("packageName")
	fileName := c.Param("fileName")

	mutex.Lock()
	defer mutex.Unlock()

	var appEntry *AppEntry
	var project *Project
	var buildFound bool

	// Find the build and remove it
	for i := range allProjects {
		for j := range allProjects[i].Apps {
			if allProjects[i].Apps[j].PackageName == packageName {
				project = &allProjects[i]
				appEntry = &allProjects[i].Apps[j]

				newBuilds := []BuildInfo{}
				for _, build := range appEntry.Builds {
					if build.FileName == fileName {
						buildFound = true
					} else {
						newBuilds = append(newBuilds, build)
					}
				}
				appEntry.Builds = newBuilds
				break
			}
		}
		if appEntry != nil {
			break
		}
	}

	if !buildFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "构建版本未找到"})
		return
	}

	// If the app has no more builds, remove the app itself
	if len(appEntry.Builds) == 0 {
		newApps := []AppEntry{}
		for _, app := range project.Apps {
			if app.PackageName != packageName {
				newApps = append(newApps, app)
			}
		}
		project.Apps = newApps
	}

	// Save metadata changes
	if err := saveMetadata(); err != nil {
		// This is tricky, a rollback would be complex. For now, log and return error.
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新元数据失败"})
		return
	}

	// Delete the physical file
	filePath := filepath.Join("uploads", fileName)
	if err := os.Remove(filePath); err != nil {
		fmt.Printf("警告: 删除文件 %s 失败: %v\n", filePath, err)
		// Don't fail the whole request, but log it.
	}

	c.JSON(http.StatusOK, gin.H{"message": "构建版本已删除"})
}

func handleDeleteApp(c *gin.Context) {
	if c.Query("password") != deletePassword {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "删除密码错误"})
		return
	}

	packageName := c.Param("packageName")

	mutex.Lock()
	defer mutex.Unlock()

	var project *Project
	var appFound bool
	var buildsToDelete []BuildInfo

	for i := range allProjects {
		newApps := []AppEntry{}
		for _, app := range allProjects[i].Apps {
			if app.PackageName == packageName {
				project = &allProjects[i]
				appFound = true
				buildsToDelete = app.Builds
			} else {
				newApps = append(newApps, app)
			}
		}
		if appFound {
			allProjects[i].Apps = newApps
			break
		}
	}

	if !appFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "应用未找到"})
		return
	}

	// If the project has no more apps, remove the project itself
	if len(project.Apps) == 0 {
		newProjects := []Project{}
		for _, p := range allProjects {
			if p.ProjectName != project.ProjectName {
				newProjects = append(newProjects, p)
			}
		}
		allProjects = newProjects
	}

	if err := saveMetadata(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新元数据失败"})
		return
	}

	// Delete all associated files
	for _, build := range buildsToDelete {
		filePath := filepath.Join("uploads", build.FileName)
		if err := os.Remove(filePath); err != nil {
			fmt.Printf("警告: 删除文件 %s 失败: %v\n", filePath, err)
		}
	}
	// Also delete the icon
	iconPath := filepath.Join("static", "icons", fmt.Sprintf("%s.png", packageName))
	if err := os.Remove(iconPath); err != nil {
		fmt.Printf("警告: 删除图标 %s 失败: %v\n", iconPath, err)
	}

	c.JSON(http.StatusOK, gin.H{"message": "应用已删除"})
}

// --- Metadata Logic ---

func updateMetadata(projectName string, appInfo AppInfo, newBuild BuildInfo) error {
	mutex.Lock()
	defer mutex.Unlock()

	var project *Project
	for i := range allProjects {
		if allProjects[i].ProjectName == projectName {
			project = &allProjects[i]
			break
		}
	}
	if project == nil {
		newProject := Project{ProjectName: projectName, Apps: []AppEntry{}}
		allProjects = append(allProjects, newProject)
		project = &allProjects[len(allProjects)-1]
	}

	var appEntry *AppEntry
	for i := range project.Apps {
		if project.Apps[i].PackageName == appInfo.PackageName {
			appEntry = &project.Apps[i]
			break
		}
	}
	if appEntry == nil {
		newAppEntry := AppEntry{
			AppName:     appInfo.AppName,
			PackageName: appInfo.PackageName,
			IconPath:    appInfo.IconPath,
			Builds:      []BuildInfo{},
		}
		project.Apps = append(project.Apps, newAppEntry)
		appEntry = &project.Apps[len(project.Apps)-1]
	} else {
		appEntry.AppName = appInfo.AppName
		if appInfo.IconPath != "" {
			appEntry.IconPath = appInfo.IconPath
		}
	}

	appEntry.Builds = append([]BuildInfo{newBuild}, appEntry.Builds...)

	return saveMetadata()
}

// --- Template Helper Functions ---

func formatSize(size int64) string {
	const (
		_  = iota
		KB = 1 << (10 * iota)
		MB
		GB
	)
	if size >= GB {
		return fmt.Sprintf("%.2f GB", float64(size)/GB)
	} else if size >= MB {
		return fmt.Sprintf("%.2f MB", float64(size)/MB)
	} else if size >= KB {
		return fmt.Sprintf("%.2f KB", float64(size)/KB)
	} else {
		return fmt.Sprintf("%d B", size)
	}
}

func first(s string) string {
	if len(s) > 0 {
		return string([]rune(s)[0])
	}
	return ""
}
