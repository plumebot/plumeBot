// 本文件提供插件指令集（PluginResult）的结构校验入口（转发 plugin-sdk/entity，单一事实来源）。
package entity

import sdkentity "github.com/plumebot/plumebot-sdk/plugin"

// ValidatePluginResult 校验插件返回的指令集：枚举合法、必填字段非空。
// 实现已迁至 plugin-sdk/entity（方案 A），此处为宿主侧转发入口，宿主代码
// 继续经 entity.ValidatePluginResult 调用。返回带位置的描述性 error（仅记录），无对应哨兵。
func ValidatePluginResult(r PluginResult) error {
	return sdkentity.ValidatePluginResult(r)
}
