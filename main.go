// mb 是一个自托管的域名 / VPS 资产面板。
//
// 用法:
//
//	mb               启动服务
//	mb healthcheck   检查本机服务是否正常(供容器健康检查使用)
//	mb version       打印版本号
package main

import (
	"fmt"
	"log"
	"os"

	_ "time/tzdata" // 内嵌时区数据:没有系统时区文件的最小镜像里,TZ 环境变量也能生效

	"mb/internal/app"
	"mb/internal/buildinfo"
	"mb/internal/config"
)

func main() {
	config.LoadDotEnv(".env")

	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "serve":
		if err := app.Run(); err != nil {
			log.Fatal(err)
		}
	case "healthcheck":
		if err := app.Healthcheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "version", "-v", "--version":
		fmt.Println(buildinfo.DisplayVersion(), buildinfo.ShortCommit())
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\nusage: mb [serve|healthcheck|version]\n", command)
		os.Exit(2)
	}
}
