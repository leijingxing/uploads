# 智能应用分发平台

这是一个使用 Go (Gin) 编写的轻量级、现代化的应用分发平台。它提供了一个简洁的 Web 界面，用于上传、管理和分发 Android (.apk) 应用。

## ✨ 核心功能

- **智能解析**: 上传 APK 后，服务器会自动解析并提取应用名称、包名 (Package Name)、版本号和应用图标。
- **简化上传**: 用户无需手动填写繁琐的应用信息，只需选择项目、输入渠道和更新日志即可。
- **图标展示**: 在列表和详情页自动展示应用图标，如果图标格式特殊无法解析，则会优雅地回退显示一个美观的占位符。
- **二维码下载**: 为每个应用版本生成二维码，方便移动设备扫码下载。
- **历史版本**: 自动维护每个应用的历史版本列表。
- **响应式设计**: 界面在桌面和移动设备上均有良好体验。

## 🚀 快速开始

### 1. 环境准备

- 安装 [Go](https://golang.org/) (版本 >= 1.18)。
- 确保您的 Go 环境已正确配置。

### 2. 安装依赖

在项目根目录运行以下命令，以下载并同步所有必要的依赖项：

```bash
go mod tidy
```

### 3. 运行服务

执行以下命令来启动 Web 服务器：

```bash
go run main.go
```

服务器启动后，您会看到以下输出：

```
服务器已启动，监听端口:1234
```

现在，您可以在浏览器中打开 `http://localhost:1234` 来访问本平台。

## 📂 项目结构

```
/
├── static/                # 存放 CSS 样式和提取的应用图标
│   ├── icons/             # 自动提取的应用图标存放于此
│   └── style.css
├── templates/             # HTML 模板文件
│   ├── index.html         # 首页 - 项目和应用列表
│   ├── details.html       # 应用详情页 - 版本历史
│   └── upload.html        # 上传页面
├── uploads/               # 存放上传的 APK 文件
├── go.mod                 # Go 模块依赖文件
├── go.sum
├── main.go                # 主程序文件 (Gin 服务器)
├── metadata.json          # 存储所有应用信息的数据库文件
└── README.md              # 本文档
```

## 🛠️ API

平台提供了一个简单的 API 用于上传文件。

- **Endpoint**: `POST /api/upload`
- **Method**: `POST`
- **Content-Type**: `multipart/form-data`

**表单字段:**

| 参数           | 类型   | 是否必须 | 描述                                   |
| -------------- | ------ | -------- | -------------------------------------- |
| `projectName`  | string | 是       | 应用所属的项目名称。                   |
| `channel`      | string | 是       | 本次构建的渠道，例如 `official`, `googleplay`。 |
| `releaseNotes` | string | 否       | 本次更新的说明。                       |
| `file`         | file   | 是       | 要上传的 `.apk` 文件。                 |

## 🔧 技术栈

- **后端**: [Go](https://golang.org/) + [Gin](https://gin-gonic.com/)
- **APK 解析**: [github.com/shogo82148/androidbinary](https://github.com/shogo82148/androidbinary)
- **二维码生成**: [github.com/skip2/go-qrcode](https://github.com/skip2/go-qrcode)
- **前端**: HTML5, CSS3 (无 JavaScript 框架)