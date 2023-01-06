package mpegts

import (
	. "m7s.live/engine/v4"
	"m7s.live/engine/v4/config"
)

type MpegtsConfig struct {
	// 定义插件的配置
	config.Subscribe
	Tinterval int
}

func (p *MpegtsConfig) OnEvent(event any) {
	switch v := event.(type) {
	case FirstConfig: //插件初始化逻辑
		plugin.Info("Started")
	// case Config://插件热更新逻辑
	case Stream: //按需拉流逻辑
		plugin.Info(v.Path)
	}
}

var plugin = InstallPlugin(new(MpegtsConfig))
