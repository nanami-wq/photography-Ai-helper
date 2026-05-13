package rag

// photographyScoreChunks 内置评分锚点语料，用于 RAG（向量或关键词）。可按业务扩展为外置 JSON/YAML 加载。
var photographyScoreChunks = []chunkrec{
	{id: "t_blur", text: "技术分锚点：明显虚焦、马赛克、强压缩导致细节不可辨认时，technique_score 应落在 15–38，dimension_notes technique 须点到「解析度/对焦」问题。"},
	{id: "t_expose", text: "技术分锚点：大面积死白死黑、高光无层次或阴影糊成团时，technique_score 不宜高于 45；若仅轻微光比问题可在 45–62。"},
	{id: "t_noise", text: "技术分锚点：噪点成片、色块断裂影响主体识别时，technique_score 倾向 25–48，并说明噪点或后期痕迹。"},
	{id: "c_cast", text: "色彩分锚点：整体偏色、灰罩或白平衡明显漂移时，color_score 多落在 22–48；须在 dimension_notes color 写明偏色方向或灰闷。"},
	{id: "c_dull", text: "色彩分锚点：饱和度极低、画面发灰发闷缺乏色阶时，color_score 不宜高于 55，除非黑白/极简题材且有意为之。"},
	{id: "c_rich", text: "色彩分锚点：色调统一、层次过渡自然、色彩服务于情绪与题材时，color_score 可给 68–88；避免无依据打满。"},
	{id: "c_oversat", text: "色彩分锚点：过饱和、溢色或色彩打架干扰主体时，color_score 通常 30–52，并写清「过艳/溢色」。"},
	{id: "co_thirds", text: "构图分锚点：主体或关键轮廓贴近三分线/黄金分割且留白有效时，composition_score 可偏高区 62–82。"},
	{id: "co_center", text: "构图分锚点：稳定中心构图、对称或仪式性居中对齐且主题契合时，composition_score 可在 55–72，不视为缺陷。"},
	{id: "co_tilt", text: "构图分锚点：明显失衡、主体贴边裁切脱脑或关键线条「压边难受」时，composition_score 倾向 18–42。"},
	{id: "co_clutter", text: "构图分锚点：信息淹没、无主次、视线无处落点时，composition_score 多在 25–48。"},
	{id: "co_negative", text: "构图分锚点：刻意留白、极简或大比例负空间服务于叙事时，勿仅因「空」扣烂分；可在 dimension_notes 解释读图路径。"},
	{id: "generic_mid", text: "通用：三项子分若均在 45–55 且 dimension_notes 多批评，须自检是否「安全中段」；应拉开与文字一致的差距。"},
	{id: "generic_consistency", text: "通用：任一 dimension_notes 与该维分数须同向；批评为主且无实质优点时，该维分通常不高于 48。"},
	{id: "m_portrait", text: "题材参考：半身/特写肖像时，眼神/面部对焦与肤色层次比极端构图技巧更影响 technique 与 color。"},
	{id: "m_landscape", text: "题材参考：风光片层次（远景清晰度、天空层次、前景引导）对 technique 与 composition 权重大于单一色彩花俏。"},
	{id: "m_still", text: "题材参考：静物与产品图对焦平面、边缘锐利度与色彩还原一致性优先，构图 cleanliness 影响 composition。"},
}

type chunkrec struct {
	id   string
	text string
}
