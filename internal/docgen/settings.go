package docgen

import (
	"fmt"
	"reflect"
	"strings"

	"tadmin/internal/store"
)

// settingDocs describes every per-group setting. docgen fails when a field
// of store.Settings has no description, so new options are always documented.
var settingDocs = map[string]string{
	"lang":      "群内语言；空值表示跟随全局 language",
	"kick_bots": "移除由非管理员拉入的机器人账号",

	"verify.enabled":    "是否对新成员启用入群验证",
	"verify.delivery":   "投递方式：overlay 浮层 / public 群内 / private 私聊",
	"verify.kind":       "题型：image 图形点选 / math 图片算术 / button 一键确认",
	"verify.timeout":    "答题时限（秒）",
	"verify.attempts":   "可尝试次数",
	"verify.on_fail":    "次数用尽后：kick 移出 / review 转人工审核",
	"verify.cooldown":   "失败移出时是否递增冷却",
	"verify.requests":   "是否处理入群申请（审批制群）",
	"verify.webapp":     "配置了 Mini App 时是否提供大图验证按钮",
	"verify.cas":        "是否查询 CAS 公共黑名单",
	"verify.ai":         "AI 资料风险：off 关闭 / hint 仅提示 / review 高风险转人工",
	"verify.review_ttl": "人工审核等待时长（秒）",

	"welcome.enabled":      "通过验证后发送欢迎语",
	"welcome.text":         "欢迎语模板；空值使用默认文案",
	"welcome.buttons":      "链接按钮，每行一排：文字 - https://…，同排用 | 分隔",
	"welcome.ephemeral":    "欢迎语是否仅新人本人可见",
	"welcome.delete_after": "自动删除延时（秒，0 表示不删除）",

	"channel.unpin":       "频道自动置顶：off 保持 / unpin 立即取消 / keep_latest 只保留最新一条",
	"channel.block_masks": "删除以其他频道身份发送的消息并封禁该频道身份",

	"clean.join":     "删除“加入群组”服务消息",
	"clean.leave":    "删除“离开群组”服务消息",
	"clean.pin":      "删除“置顶了消息”服务消息",
	"clean.other":    "删除改名、头像、语音通话等服务消息",
	"clean.commands": "自动删除管理员命令与机器人的回复",

	"raid.threshold": "突击防护阈值（每分钟入群人数，0 表示关闭）",
	"raid.minutes":   "防护持续分钟数",
}

var sectionTitles = map[string]string{
	"": "通用", "verify": "🔐 入群验证", "welcome": "👋 欢迎语", "channel": "📢 频道联动", "clean": "🧹 消息清理", "raid": "🛡 突击防护",
}

type settingField struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Default any    `json:"default"`
	Desc    string `json:"desc"`
}

type settingSection struct {
	Key    string         `json:"key"`
	Title  string         `json:"title"`
	Fields []settingField `json:"fields"`
}

// settingsDoc walks store.Settings by reflection, pairing each JSON field
// with its default value and description.
func settingsDoc() ([]settingSection, error) {
	def := store.DefaultSettings()
	root := settingSection{Key: "", Title: sectionTitles[""]}
	var out []settingSection
	var missing []string
	rv := reflect.ValueOf(def)
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		name := jsonName(rt.Field(i))
		fv := rv.Field(i)
		if fv.Kind() == reflect.Struct {
			sec := settingSection{Key: name, Title: sectionTitles[name]}
			for j := 0; j < fv.NumField(); j++ {
				sub := jsonName(fv.Type().Field(j))
				key := name + "." + sub
				desc, ok := settingDocs[key]
				if !ok {
					missing = append(missing, key)
				}
				sec.Fields = append(sec.Fields, settingField{Key: sub, Type: fv.Field(j).Kind().String(), Default: fv.Field(j).Interface(), Desc: desc})
			}
			out = append(out, sec)
			continue
		}
		desc, ok := settingDocs[name]
		if !ok {
			missing = append(missing, name)
		}
		root.Fields = append(root.Fields, settingField{Key: name, Type: fv.Kind().String(), Default: fv.Interface(), Desc: desc})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("settings without documentation in internal/docgen/settings.go: %s", strings.Join(missing, ", "))
	}
	return append(out, root), nil
}

func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" {
		return f.Name
	}
	return name
}
