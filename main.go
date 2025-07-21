package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"image/png"
	"io/ioutil"
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
	data, err := ioutil.ReadFile(metadataFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			allProjects = []Project{}
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &allProjects)
}

// saveMetadata saves the metadata to the JSON file.
// IMPORTANT: It does NOT lock the mutex, assuming the caller has already acquired a lock.
func saveMetadata() error {
	data, err := json.MarshalIndent(allProjects, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(metadataFilePath, data, 0644)
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
		c.HTML(http.StatusOK, "index.html", gin.H{
			"AllProjects":  allProjects,
			"UploadStatus": c.Query("upload"),
		})
	})

	// NEW: App Detail Page Route
	router.GET("/app/:packageName", handleAppDetailPage)

	// Upload page and API routes remain the same
	router.GET("/upload", func(c *gin.Context) {
		c.HTML(http.StatusOK, "upload.html", nil)
	})
	router.POST("/api/upload", handleApiUpload)
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

	fmt.Println("服务器已启动，监听端口:1234")
	router.Run(":1234")
}

// NEW: Handler for the App Detail Page
func handleAppDetailPage(c *gin.Context) {
	packageName := c.Param("packageName")
	var foundApp *AppEntry

	// Find the correct app across all projects
	for _, project := range allProjects {
		for _, app := range project.Apps {
			if app.PackageName == packageName {
				foundApp = &app
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
		"App":     foundApp,
		"BaseURL": baseURL,
	})
}

// handleApiUpload and updateMetadata remain the same
// AppInfo holds information extracted from an APK
type AppInfo struct {
	AppName     string
	PackageName string
	Version     string
	IconPath    string
}

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

	// --- Save APK file first ---
	filename := filepath.Base(file.Filename)
	savePath := filepath.Join("uploads", filename)
	if err := c.SaveUploadedFile(file, savePath); err != nil {
		fmt.Printf("保存文件到 %s 错误: %v\n", savePath, err)
		c.String(http.StatusInternalServerError, "保存文件错误: %s", err.Error())
		return
	}
	fmt.Printf("文件成功保存到: %s\n", savePath)

	// --- APK Parsing Logic (Corrected) ---
	pkg, err := apk.OpenFile(savePath)
	if err != nil {
		c.String(http.StatusInternalServerError, "解析APK失败: %s", err.Error())
		return
	}
	defer pkg.Close()

	appName, _ := pkg.Label(nil)
	packageName := pkg.PackageName()
	version, _ := pkg.Manifest().VersionName.String()

	icon, err := pkg.Icon(nil)
	var iconPath string
	if err != nil {
		fmt.Printf("警告: 无法提取应用 '%s' 的图标: %v\n", appName, err)
		iconPath = "" // Set icon path to empty on error
	} else {
		// Save the icon to a static file
		iconDir := filepath.Join("static", "icons")
		if err := os.MkdirAll(iconDir, 0755); err != nil {
			c.String(http.StatusInternalServerError, "无法创建图标目录: %s", err.Error())
			return
		}
		iconPath = filepath.Join(iconDir, fmt.Sprintf("%s.png", packageName))
		iconFile, err := os.Create(iconPath)
		if err != nil {
			c.String(http.StatusInternalServerError, "无法创建图标文件: %s", err.Error())
			return
		}
		defer iconFile.Close()
		if err := png.Encode(iconFile, icon); err != nil {
			c.String(http.StatusInternalServerError, "无法编码图标为PNG: %s", err.Error())
			return
		}
		fmt.Printf("应用图标已保存到: %s\n", iconPath)
	}

	appInfo := AppInfo{
		AppName:     appName,
		PackageName: packageName,
		Version:     version,
		IconPath:    filepath.ToSlash(iconPath),
	}
	fmt.Printf("APK解析结果: %+v\n", appInfo)

	buildInfo := BuildInfo{
		Version:      appInfo.Version,
		Channel:      channel,
		ReleaseNotes: releaseNotes,
		FileName:     filename,
		FileSize:     file.Size,
		UploadTime:   time.Now().Format("2006-01-02 15:04:05"),
		DownloadURL:  fmt.Sprintf("/downloads/%s", filename),
	}

	if err := updateMetadata(projectName, appInfo, buildInfo); err != nil {
		fmt.Printf("更新元数据错误: %v\n", err)
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
		project = &Project{ProjectName: projectName}
		allProjects = append(allProjects, *project)
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
		appEntry = &AppEntry{
			AppName:     appInfo.AppName,
			PackageName: appInfo.PackageName,
			IconPath:    appInfo.IconPath,
		}
		project.Apps = append(project.Apps, *appEntry)
		appEntry = &project.Apps[len(project.Apps)-1]
	} else {
		// Update app name and icon path in case they have changed
		appEntry.AppName = appInfo.AppName
		appEntry.IconPath = appInfo.IconPath
	}

	appEntry.Builds = append([]BuildInfo{newBuild}, appEntry.Builds...)

	return saveMetadata()
}

// formatSize converts file size in bytes to a human-readable string.
func formatSize(size int64) string {
	const (
		_  = iota
		KB = 1 << (10 * iota)
		MB
		GB
	)
	switch {
	case size >= GB:
		return fmt.Sprintf("%.2f GB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.2f MB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.2f KB", float64(size)/KB)
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// first returns the first character of a string.
func first(s string) string {
	if len(s) > 0 {
		return string([]rune(s)[0])
	}
	return ""
}