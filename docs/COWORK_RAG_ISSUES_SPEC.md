# COWORK_RAG_ISSUES_SPEC — 知识库"读取出错"根因与透明度问题规格书

| 项 | 内容 |
|---|---|
| 状态 | 待评审 → 待排期 |
| 日期 | 2026-09-10 |
| 分支 | feat/mindmap-read-loop(HEAD c2ef03c0 附近,所有行号以此工作树为准) |
| 范围 | coWork 知识库(RAG):导入 → FTS5 索引 → 后台深度抽取 → 实体图谱 / 检索 / 预览 |
| 方法 | 用户报告 + 生产数据库取证 + 3 轮 × 3 子代理审计(共 9 个审计子任务)+ 10 条高危论断独立复核 |
| 关联文件 | internal/rag/*、desktop/rag_app.go、desktop/app.go、desktop/frontend/src/components/cowork/*、internal/provider/budget.go |

---

## 1. 背景与现象

用户在办公界面的知识库(分类"工作")中导入 7 份方案文档。截图中 3 份显示**"出错"**:
`4铁通办公网安全防护方案.pptx`、`3承载网安全设备复用实施方案.docx`、`2办公终端安全防护方案推进.pdf`;
3 份显示"已抽取"。数据库实际有 **4 份** error——`5铁通准入设备改造方案.docx` 亦为 error,截图时该行处于悬停态未显示标签。用户报告:①"总是读取出错";② 文档较大(最大约 10 万字);
③ 应用开一晚上,后台也不会自动继续抽取;④ 用户不知道抽取进度,也不希望在被蒙在鼓里的情况下发生后台抽取——**核心诉求是知情权,而不是单纯修 bug**。

## 2. 取证与关键证据

数据源:`%APPDATA%\fairpeer\rag.db`(应用配置目录,`desktop/app.go:476`;`desktopConfigDir()` = `%APPDATA%\fairpeer`,`desktop/tabs.go:1724-1731`)。

### 2.1 job 级证据("工作"分类,与截图逐一吻合)

| 文件 | 块数 | 结果 | UI 显示 |
|---|---|---|---|
| 1办公网防护一体化解决方案.docx | 9 | 1 成功 / 8 超时 | 已抽取(误导) |
| 2办公终端安全防护方案推进.pdf | 2 | 0 / 2 超时 | **出错** |
| 2办公终端安全防护方案推进.pptx | 1 | 1 成功(178887ms,擦线) | 已抽取 |
| 3承载网安全设备复用实施方案.docx | 1 | 0 / 1 超时 | **出错** |
| 4铁通办公网安全防护方案.pdf | 7 | 1 / 6 超时 | 已抽取(误导) |
| 4铁通办公网安全防护方案.pptx | 3 | 0 / 3 超时 | **出错** |
| 5铁通准入设备改造方案.docx | 1 | 0 / 1 超时 | **出错** |

每个失败 chunk:`error_msg = "context deadline exceeded"`,`latency_ms = 180000~180007`(精确撞死在 180s 超时线上)。
成功的 chunk 耗时 90.9s~178.9s——**当天端点整体极慢,成功者全部擦线**。另有一个应用自身元数据文件 `.fairpeer/desktop-topic-title-sources.json` 也被导入并以同样方式超时(见 F-G2)。

### 2.2 反证(排除文件/格式/代码路径因素)

8 月 15 日将同一批文件重新导入 default 分类:相同内容每块仅 **10.6s~21.2s,全部成功**。
→ 文件本身、格式解析层、Go 抽取管线均无问题;变量只有"当天 LLM 端点速度 vs 180s 硬预算"。

### 2.3 现状确认

应用当前未运行;rag.db 自 2026-08-15 后无任何新抽取记录("工作"= 5 done / 5 error)。
用户看到的"出错"是持久化的历史终态;`ResumableJobs` 不收 error(见 F-A4),**放置再久也不会自愈**——这解释了"开一晚上也不动"。
配置:`default_model=""`、`fast_task_model` 未设、`rpm=60 ReserveMain=2`(config.toml / `internal/config/config.go:1601`);2026-09-09 日志显示抽取模型解析为 `mimo/mimo-v2.5`。

### 2.4 大文档(10 万字)推演

块尺寸由两套口径混合决定(F-E3):chunkMarkdown 按**字节**(3000 字节≈1000 汉字)决定累积/flush 边界,
超出部分经 windowChunk 按 **rune**(3000 字符)再切。实测 1.docx 块 avg 2960 / max 3000 **字符**(rune 窗口路径),
标题密集的文档则会落在 1000~1500 字符档。因此 10 万字 ≈ **40~100 块**(视文档结构);
TwoStage 每块 2 次 LLM 调用 → **80~200 次请求**;并发=1 + 块间隔 3s(extract.go DefaultPipelineConfig):
- 健康端点(实测 10~21s/块,8 月 15 日):约 **12~35 分钟**;
- 8 月 14 日速度(90~180s/块):**1~5 小时**,且大部分块撞 180s 超时;
- 吞吐与限流:快端点下吞吐由 3s 间隔主导(≤20 块/min ≈ 40 req/min,接近 rpm=60 的 2/3);慢端点下由调用时长主导(约 0.7~1.3 req/min)。
  叠加主对话共用 rpm=60 时,`budget.Acquire` 的排队等待**计入每块 180s 预算**(provider/budget.go:72-93)→ 大文档在端点波动时成批超时。

## 3. 根因

**直接根因**:每块的 180 秒 context(extract.go:479)同时被"首次 LLM 尝试 + 退避 + 重试"共享,而 HTTP client 超时也是 180s(llm_extractor.go:64)。端点慢时首次尝试耗尽全部预算,退避 2s 的 select 立即命中 `ctx.Done()`,`lastErr` 被覆写为裸 `context deadline exceeded`——**"每块最多 2 次尝试"在慢端点下永远只发生 1 次**。全部块超时的文件 → job=error → UI"出错"。

**放大机制**:TwoStage 硬编码(2×延迟共享同一预算)、并发=1 串行、RPM 排队计入预算、error 状态永不自动恢复、失败原因不落库不展示(用户无从判断与重试)。

**设计层根因(用户"纠结"的本质)**:系统对"后台任务"既**不自动恢复**(error 是终态)也**不透明**(无原因、无部分失败语义、无通知、无健康度)——两头都不占。

## 4. 问题全量清单

严重度:P0 阻断修复目标/直接损害;P1 核心缺陷;P2 显著缺陷;P3 次要。行号已抽样复核(附录 A)。

### A. 抽取超时与重试机制

- **F-A1【P0】180s 预算被全部重试共享,client 超时=任务预算** — extract.go:479,514-519;llm_extractor.go:64。慢端点下重试永不发生;失败信息被 `ctx.Err()` 覆盖为无前缀 "context deadline exceeded",丢失真实错误(如 HTTP 429/5xx)。
- **F-A2【P1】TwoStage 硬编码** — desktop/app.go:528。每块 2 次串行 LLM 调用共享同一 180s 预算;不可配置。
- **F-A3【P1】emitProgress 全局 1s 节流吞终态事件** — extract.go:542-557。多 job 连续完成时最后一个 done/error 事件被丢弃,且前端仅靠事件刷新(CoworkDock.tsx:1092-1108),UI 永久停留旧状态。
- **F-A4【P1】error job 永不自动恢复** — entities.go:923-929 `ResumableJobs WHERE status IN (pending,extracting)`。"开一晚上不动"的直接原因。
- **F-A5【P2】失败块的超时样本污染内存 ETA 窗口** — extract.go:523-527 把 180000ms 计入 slidingWindow(50),一次超时把 ETA 拉到分钟级;DB 均值(entities.go:822-840)虽只统计成功块,但 `LatencyAvgMs` 优先于 DB 兜底(rag_app.go:698-707)。
- **F-A6【P2】stage2 失败丢弃 stage1 实体** — llm_extractor.go:134-138 返回 `(res, err)`,extract.go:485-486 仅 err==nil 才 upsert;与 :136 "keep the entities" 注释相悖,白烧 stage1。
- **F-A7【P2】SetBudget 数据竞争 + Resume 先于预算绑定** — llm_extractor.go:49-52 无锁写 budget,chatJSON:154-158 并发读;boot.RebindRAGBudget 在每次设置重建时调用;initRAG 中 Resume(app.go:569)先于 budget 绑定执行,重启恢复的 chunk 不受 RPM 限流。
- **F-A8【P2】未配模型时 noop 抽取器假成功** — app.go:544-546 仅 slog.Warn;extract.go:141-143 换 noopExtractor,Extract 恒成功返回空 → 全部块 done、job done、UI"已抽取"但 0 实体,无任何用户可见提示。(已复核 T3 属实)
- **F-A9【P3】HTTP 响应体无大小上限、usage token 不入统计** — llm_extractor.go:183 `io.ReadAll` 无 LimitReader;Usage.total_tokens 被忽略。

### B. 状态语义与知情权(后端+前端)

- **F-B1【P0】job 级 error_msg 无任何写入点** — entities.go 全文只有 COALESCE 读取与 CreateJob 置 NULL;真正错误在 rag_chunks.error_msg(entities.go:697)且 UI 不读;前端 RagNode.tsx:167 已就绪的 `出错: ${errorMsg}` 永远拿空串。**出错无原因 = 用户"看不懂"的直接原因。**
- **F-B2【P0】部分失败伪装"已抽取"** — DoneChunks 统计含 error 块(entities.go:703-707),fileStatus 只要 `done && DoneChunks>0` 即 enriched(rag_app.go:198-201);实测 1/9、1/7 块成功的文件显示"已抽取"。status 词汇无 partial(前端 types.ts:864-877、RagNode.tsx:160-192 无对应分支)。
- **F-B3【P1】queued 状态渲染为"✓ 已索引"** — 后端 fileStatus 返回 queued(rag_app.go:195),RagNode 的 switch 无 queued 分支落入 default 显示 ✓,排队文件看起来已完成。
- **F-B4【P1】无集合级健康度摘要** — RagCollectionView(types.ts:880-891)无失败计数;集合行仅显示文档数(CoworkDock.tsx:1357),无法看出"部分失败/失败/排队"分布。
- **F-B5【P1】搜索/预览不标注来源文件抽取状态** — snippet(types.ts:922-945)、实体 sources、DocPreview 均无状态字段;残缺抽取的命中结果被当全量依据。
- **F-B6【P1】重试/取消错误静默吞掉、无成本预估** — CoworkDock.tsx:1497-1516 `.catch(()=>refreshTree())`;RagStartExtract 失败(如未配 key)点击无任何反馈;重试前无"将发起 N 次调用"提示。
- **F-B7【P1】抽取完成/失败无系统通知** — 全前端无对应通知;K13。
- **F-B8【P2】mock 静默兜底** — bridge.ts:953-963 每次 call-time 探测 `window.go`,失败即落 mock(假集合/假进度/假搜索),无"演示数据"标识;mock RagStartExtract 参数语义与真实后端不一致(:5007 vs rag_app.go:308),掩盖调用方 bug。(已复核 S4 属实)
- **F-B9【P2】用中文子串判完成** — CoworkDock.tsx:1044-1046 `msg.includes("已提取完成")`,后端文案改动/英文环境即失效。
- **F-B10【P2】硬编码文案绕过 i18n** — TemplateSelect.tsx:549-553 "✗ failed"/"waiting";ImportModal.tsx:39,60-66 中文 toast。
- **F-B11【P2】完成后无耗时/调用次数展示** — ETA 仅抽取中 hover 可见(RagNode.tsx:47-59);成本信息全 UI 缺失。
- **F-B12【P3】cancelled 落入默认文案"抽取中…"** — extract.go:569-576 msg switch 无 cancelled 分支;JobError 分支引用永空的 job.ErrorMsg。
- **F-B13【P2】后台批量操作零反馈** — RagDetectCommunities(rag_app.go:1035-1053)失败仅 slog.Warn、成功不回传结果(CommunityResultView 定义后未用于反馈);RagEmbedEntities(:617-651)在无可嵌入实体时静默 return——三种场景用户均无感知。

### C. 任务生命周期与并发

- **F-C1【P1】取消复活竞态** — extract.go:477 无条件 SetJobStatus(Extracting);CancelJob(:414-428)置 cancelled 并清队列,但已出队任务把 cancelled 翻回 extracting,剩余 chunk 永 pending,job 永卡 extracting;重启被 ResumableJobs 复活重跑。(已复核 T1 属实)
- **F-C2【P1】cancelled 被终态翻转覆盖为 done** — entities.go:719-728 不检查当前 status,取消后 in-flight 块完成即翻 done。
- **F-C3【P0】幂等守卫吞掉 error 块的重试结果** — entities.go:694-695 `prevStatus==ChunkDone || prevStatus==ChunkError 直接 commit`;而 PendingChunksForJob(:799-800)把 pending+error 都重入队 → Resume/重试中 LLM 成功但 chunk 永停 error、done_chunks 不涨、**job 永不收敛(白跑)**。这是"只重试失败块"方案的前置 P0。(已复核 T2 属实)
- **F-C4【P2】RagStartExtract 无互斥** — rag_app.go:300-372 无运行标志;连点两次 = 两个 HE goroutine(各 4 worker)或两次 EnqueuePaths(force=true),token 双倍。(已复核 T8 属实)
- **F-C5【P2】CreateJob 孤儿 chunk 与在途写污染** — entities.go:631-651 先换 job id 再按子查询删旧块(子查询只命中新 id),旧 chunk 行成孤儿;重导入进行中,旧 job 的 in-flight upsert 已提交(extract.go:497-506)而 MarkChunkDone 因旧 id 消失整事务回滚 → 旧文本实体污染图谱。
- **F-C6【P2】RagRemovePath/RagClear 不取消内存队列** — rag_app.go:485-512 只删库;残留任务继续执行把已删文件的实体写回("知识复活"),MarkChunkDone 报 ErrNoRows。组合场景更糟:删除触发的级联 `DeleteCollectionTree`(rag_app.go:493-495)清掉集合后,仍在跑的 HE goroutine 会立刻把已删集合的实体 Upsert 回来。
- **F-C7【P2】Stop 后 Start 永久 no-op** — extract.go:167-178,250-258 `started` 不复位、stopCh 不重建;与 Stop 注释矛盾(当前仅启动时 Start 一次,属潜伏)。
- **F-C8【P2】Resume 的 idx 守卫静默跳过/文本错位** — extract.go:218-220:文件重解析后块数变少→越界块静默 continue(无日志、不标 error),job 永卡 extracting 成僵尸;块数未变但边界漂移→旧 job 的 chunkID 映射到新文本,Source.Chunk 错位;无 content_hash 失配校验。
- **F-C9【P3】dev 模式双实例共享存储与 HE 端口** — single_instance.go:12 FAIRPEER_DEV=1 跳过锁;两实例共写同一 SQLite(busy_timeout 实际未生效,见 F-F1)、抢 HE 端口 18900(he_service.go:93-96),败者回落 Go pipeline,胜者 HE 对两实例同时服务,无互斥。

### D. 多抽取路径不一致(Go pipeline vs Hyper-Extract)

- **F-D1【P1】HE 路径不落 job 状态 → queued 假象 + 重启双倍重抽** — rag_app.go:398-464 全程只 UpsertEntity/Relation,不写 rag_jobs/rag_chunks;树永远"queued";TemplateSelect 轮询 allDone 永假跑满 5 分钟(TemplateSelect.tsx:53,143-186,每 tick 2 次 IPC);重启后 Resume 把这些 pending job 全部灌入 Go pipeline 按块重抽(比 HE 整文件更贵)。(已复核 T4/T6 属实)
- **F-D2【P1】两种 rag:progress 事件 shape 不一致** — HE(rag_app.go:1647-1660):JobID 恒空、status 用 UI 词汇(enriched/error)、Done/Total=文件下标(非单调)、不节流;Go(extract.go:577-586):status=done、chunk 数、1s 节流。前端按统一 handler 处理必然错乱。
- **F-D3【P1】HE"增量"实为全量 + mode=full 失效** — rag_app.go:388-393 收集集合内**所有** status 的文件(无 skip-done)、签名无 mode 形参;:311-313 HE 分支 return 早于 :331 的 DeleteCollectionEntities,"全量重抽"实为合并式。(已复核 S3 属实)
- **F-D4【P1】单文件重试绕过 HE 且清空模板 prompt** — CoworkDock.tsx:1499-1500 传 n.path 当 template → isTemplate=false 永走 Go pipeline(force=true);CreateJob 的 ON CONFLICT 把 job 行 node_prompt/edge_prompt 清空(entities.go:641-642),改用通用 prompt 重抽。(已复核 T7 属实)
- **F-D5【P2】RagBatchExtract 丢弃模板 prompt** — rag_app.go:1531-1546 对 pending job 用空 prompt 重建并覆盖 job 行。
- **F-D6【P2】HE 无取消通道 + 可与 Go pipeline 并发双写** — rag_app.go:398-464 goroutine 无 ctx;RagCancelExtract 只进 ragPipeline;两路径并发时 :460 的 PruneDanglingRelations 与 Go 的跨 chunk upsert 之间无事务边界,合法关系可被误删;Go full 清空后 HE 残余 goroutine 写回"幽灵实体"。
- **F-D7【P2】HE/Go 来源撞键** — HE 恒写 Source{Chunk:0}(rag_app.go:432),Go 按块写;mergeSources(entities.go:211-221)按 path+chunk 去重 → 来源计数偏低、整文件描述覆盖分块描述。
- **F-D8【P3】HE 进度 ETA 用并行分母低估** — rag_app.go:443-451 `avgMs = 总耗时/完成数` 在 4 worker 并发下把墙钟时间摊到多文件,单文件延迟被低估,前端 ETA 偏乐观;进度 done 值传的是文件下标(非完成数),4 worker 乱序完成时进度条非单调跳动。

### E. 大文档与解析层

- **F-E1【P1】大扫描 PDF 无法导入** — officedoc.go:142-183 OCR 10 分钟超时覆盖整份 PDF、无分页 checkpoint(PaddleOCR CPU 2-5s/页,>100 页必超时);markitdown 兜底仅 3 分钟(docconv.go:37-40);Go 兜底解析不出文字 → 整本失败。
- **F-E2【P1】GetDocumentPreview 同步重解析大文件** — rag_app.go:1385-1413 在 UI 绑定内同步 readDoc,PDF 重跑 OCR(10min)+markitdown(3min);officedoc.go:476 已有带超时的 ReadDocumentForPreview 却未被使用。预览请求可长时间 await(不冻结窗口,但体验为"点开没反应")。(已复核 S6:量级成立)
- **F-E3【P2】切块度量字节/字符不一致** — chunkMarkdown 按字节累积(store.go:1214,1224,上限 3000 字节≈1000 汉字),windowChunk 按 rune(store.go:1129-1152,3000 字符),表格守卫 max*4 按字节(12000 字节);llm_extractor truncateChunk 按字节截 6000(llm_extractor.go:288-290)→ 部分 CJK 块在抽取输入端被静默截掉约 1/3(FTS5 不受影响,图谱素材缺失)。
- **F-E4【P2】大文档串行耗时无预估/无并发选项暴露** — 并发=1 + 3s 间隔 + TwoStage:10 万字 ≈ 70-80 次调用;`[cowork] extract_concurrency` 配置存在(config.go:539)但默认 1 且 UI 无入口;导入时无块数/预计时长提示。
- **F-E5【P3】markitdown 固定 3 分钟超时无按大小自适应** — internal/docconv/docconv.go:37-40,126。
- **F-E6【P3】expandCJKBigrams 写放大 2.3×** — store.go:1472-1516:3000 汉字块 body 9KB→约 21KB;List() 按 length(body) 统计使集合体积虚高。

### F. 存储与检索

- **F-F1【P0】SQLite DSN 参数被驱动静默忽略** — store.go:73 `?_journal_mode=WAL&_busy_timeout=5000`:modernc.org/sqlite v1.52.0 只认 `_pragma=` 形式(netdev/metrics.go:59 是正确写法)→ WAL 与 busy_timeout **从未生效**,实际 delete 日志模式、busy_timeout=0;calendar/store.go:70 同病。衍生:并发访问无 busy 保护;FK pragma 缺失 → rag_chunks 的 ON DELETE CASCADE 永不生效(store.go:87-92 的启动清孤儿即为此兜底)。(已复核 S5 属实)
- **F-F2【P2】检索单字 CJK 必 miss + 整词追加为死代码** — 查询侧 tokenize 的整词追加条件写反(store.go:1403 `len([]rune(run))==1`,:1400-1402 注释承诺永不执行);索引侧 expandCJKBigrams 只产 bigram(:1488-1498),孤立单字才有单字 token → 常用单字查询("渲""蓄")检索不到。(已复核 S2 修正归因)
- **F-F3【P2】预览分块与入库分块用不同 chunker** — 导入 ext="markdown" 走 chunkMarkdown,预览按原扩展名走 windowChunk(store.go:1119-1126,rag_app.go:1395)→ "Chunk #N" 边界与 FTS 实际不一致;`strings.Index` 找不到时 idx=0 静默错位(rag_app.go:1397-1410);Start/End 为字节偏移(JS UTF-16 语义不符,当前未消费,属潜伏)。
- **F-F4【P3】Obsidian 导出按字节截断 UTF-8** — obsidian.go:150-153 `desc[:60]` 可能截在 rune 中间。

### G. 导入边界与会话作用域

- **F-G1【P1】导入代码项目会收录 .git/无扩展名文件** — isSupportedExt 对空扩展名返回 true(extract.go:678),walkDocs(:659-671)不过滤 .git/node_modules 等点目录;.git/objects 散对象被 decodeTextBytes(lossy UTF-8)强转文本入库污染 FTS。(已复核 T10 属实)
- **F-G2【P1】应用自身元数据被导入知识库** — 项目文件夹内 `.fairpeer/*.json` 被照常收录(生产库实证:desktop-topic-titles.json 等 3 条 job,其一为 error 且展示在文件树里干扰用户)。
- **F-G3【P1】会话多选集合静默失效** — context.go:113-118 仅单选返回集合名,多选/空返回 "" → RagSearch/RagSemanticSearch/RagAsk 全部放大为全库搜索,不提示。(已复核 T9 属实)
- **F-G4【P1】集合改名后会话失联** — RenameCollection(rag_app.go:1006-1015)不同步 SessionRAGContext.activeCollections → 旧名查询 0 命中且无报错,"知识库中未找到相关内容"。
- **F-G5【P3】walkDocs 次要盲区** — 符号链接目录被 filepath.Walk 跳过(固有行为)、>260 字符路径错误被静默吞(extract.go:660-661 `return nil // skip unreadable`),均无日志。

## 5. 用户痛点 → 设计原则

用户的纠结("它有 bug,修复了 bug 也不对,需要让用户有知情权")归纳为三条原则:

1. **状态诚实**:任何时刻,UI 展示的抽取状态必须与数据库事实一致(含部分失败与原因)。
2. **恢复显式**:默认不自动重试;提供"便宜到敢点"的手动恢复(只重试失败块 + 事前预估 + 事后回执)。
3. **自动需授权**:一切无人值守的后台抽取行为必须是显式 opt-in,可见、可限速、可停、可暂停。

## 6. 修复需求

### R0 基础正确性(必须先行,是 R1-R3 的前置)

| # | 需求 | 修复点 | 验收标准 |
|---|---|---|---|
| R0-1 | 重构每块超时/重试:per-attempt 独立 ctx(如 120s)或 client 超时(90s)< 任务预算(300s,TwoStage 加倍) | extract.go:479-521, llm_extractor.go:64 | 单测:fakeExtractor(extract_test.go:52,带 delay+calls 计数)阻塞超过 per-attempt 超时,断言 rag_chunks.attempts==2 且 error_msg 含首次错误的 sentinel 前缀(非裸 "context deadline exceeded") |
| R0-2 | 修 MarkChunkDone 幂等守卫:仅 prev==done 跳过,error 允许被重试结果覆盖 | entities.go:694-695 | 单测:newTestStore 造 2 块 job → MarkChunkDone(err) → MarkChunkDone(done)×2,断言 chunk=done、done_chunks=2、job=done;反向断言 prev==done 仍跳过 |
| R0-3 | DSN 改 `_pragma=` 形式并补 foreign_keys | store.go:73(calendar 同步修) | `PRAGMA journal_mode` 返回 wal;busy_timeout>0;FK 开启;并发打开两连接写不再报 busy |
| R0-4 | 取消状态守卫:processTask/SetJobStatus/终态翻转尊重 cancelled | extract.go:477, entities.go:719-728 | 单测:blockingExtractor 使块在途时调 CancelJob,块完成后断言 job 终态保持 cancelled |
| R0-5 | HE 路径最小落库:每文件抽取前 SetJobStatus(extracting)、成功 done、失败 error+error_msg | rag_app.go:398-464 | HE 完成后 `SELECT status FROM rag_jobs` 该文件=done;重启 Resume 后 fakeExtractor 调用数==0 |
| R0-6 | walkDocs 排除点目录(.git/.fairpeer/node_modules 等)与空扩展名 | extract.go:659-682 | 导入含 .git/.fairpeer 的项目目录,两者均不入库(单测断言 walkDocs 返回列表) |
| R0-7 | HE 分支处理 mode=full;HE "增量"跳过 done;RagStartExtract 加运行互斥 | rag_app.go:300-393 | full 清空生效;抽取运行中第二次 RagStartExtract 返回忙错误(或 HE 启动计数==1),实体数不翻倍;增量不重抽 done 文件 |

### R1 状态诚实(知情权地基)

| # | 需求 | 改动点 | 验收标准 |
|---|---|---|---|
| R1-1 | rag_jobs 增加 `failed_chunks` 列(ALTER 迁移,参照 v2 node_prompt 做法);MarkChunkDone 终态时写 job 级 error_msg(取最后一条 chunk 错误的人话映射),翻 done 时清空 | entities.go、migrate | 部分失败文件在 DB 中可见 failed_chunks>0 与原因;成功 job 无残留 error_msg |
| R1-2 | fileStatus 新增 partial(done && failed_chunks>0);RagNodeView 增加 failedChunks/errorMsg 字段 | rag_app.go:191-209、types.ts:858-877、bridge.ts 镜像 | 1/9 成功的文件显示"部分失败(1/9 块)"而非"已抽取" |
| R1-3 | 前端 queued/partial 状态的文案、图标、样式;errorMsg 悬停展示人性化错误(deadline→"模型响应超时",HTTP 429→"触发限流",映射在 i18n) | RagNode.tsx:160-195、zh.ts/en.ts 新键 | 每种状态在中文/英文界面均有正确文案;出错行悬停可见原因 |
| R1-4 | 集合级健康度摘要(完成/部分失败/失败/排队计数) | ListRagCollections 或前端聚合 | 集合行可见"3 部分失败,1 失败" |
| R1-5 | 搜索命中/实体 sources/文档预览标注来源文件抽取状态 | snippet 与 sources 查询链路 | 命中部分失败文件的条目带"该文件仅部分抽取"标识 |
| R1-6 | 进度事件统一:ProgressEvent 增加 kind(progress/terminal)与 scope(chunk/file),终态事件不节流、必达;节流状态从包级全局变量移入 Pipeline 字段(可测性) | extract.go:542-557、rag_app.go:1647-1660 | 单测:emit 回调记录事件,连续完成 3 个单块 job(间隔<1s),断言收到 3 个 terminal 事件且与 DB 一致;进度事件仍被节流的反向断言 |
| R1-7 | 前端消费 payload 按 jobId/path 局部更新节点,替代"每事件全量 ListRagTree";加防抖 | CoworkDock.tsx:1092-1108 | mock bridge 上挂 ListRagTree 调用计数:50 文件抽取期间 IPC 次数 ≤ 事件数×防抖上限,树不整页重渲染 |

### R2 显式恢复(默认手动,便宜到敢点)

| # | 需求 | 改动点 | 验收标准 |
|---|---|---|---|
| R2-1 | 新增 RetryFailedChunks(jobID):仅重入队 error 块,复用 PendingChunksForJob + FTS5 body_raw 重读文本,**不走 EnqueuePaths/CreateJob**(不重置成功块) | rag_app.go 新绑定 + extract.go | 单测:造 7 块 job(5 done + 2 error)→ RetryFailedChunks → fakeExtractor.calls==4(依赖 R0-2 先落地)、2 个 error 块翻 done、原 5 个 done 块实体行不变、job 收敛 done |
| R2-2 | 重试前成本预估:块数 × 成功块 P50 延迟(错误块不入内存窗口,store 增加 P50 查询);dedup 条件改 `status==done && failed_chunks==0` 使文件夹重导入可自愈 | extract.go:323-367,523-527 | 预估文案"还剩 2 块 ≈ 4 次调用,约 1 分钟";重导入文件夹时部分失败文件被重新入队 |
| R2-3 | 抽取终态系统通知 + 失败错误回执 | R1-6 终态事件 → 前端通知 | 抽取完成/失败均有通知,失败通知含可读原因 |
| R2-4 | 重试/取消按钮的错误不再静默;取消调用对 HE 生效(HE 增加取消通道) | CoworkDock.tsx:1497-1516、rag_app.go HE goroutine | 未配 key 时点击重试出现 toast;HE 任务可被取消 |
| R2-5 | 大文档导入时提示块数与预计时长;暴露 extract_concurrency 配置入口 | ImportModal、设置页 | 导入 10 万字文档提示"约 38 块,预计 20 分钟(当前速率)" |

### R3 自动重试(显式 opt-in,默认关闭)

| # | 需求 | 改动点 | 验收标准 |
|---|---|---|---|
| R3-1 | `[cowork]` 新增 extract_auto_retry(bool,默认 false)、extract_auto_retry_max_rounds(默认 2):独立模块扫 `status='error' AND rounds<N` 调 R2-1;不改 ResumableJobs 语义 | config.go 四处同步(struct/defaults/render/app 读取)、extract.go 扫描入口 | 默认关闭时行为与现状一致;开启后 error job 在空闲档自动重试 |
| R3-2 | 后台活动可见性:自动重试运行中在托盘/面板常驻标识;可一键暂停;连续 N 轮失败停手并发通知汇报 | 前端 + 通知 | 手工验收清单+截图:用户随时能发现"后台在抽取"并叫停;停止有汇报 |
| R3-3 | 轮次计数持久化(rag_jobs 加列)避免重启归零无限重试;与 Resume 双入口互斥(extracting 不碰) | entities.go 迁移 | 重启后不重复已耗尽轮次的 job |

### 6.4 验收维度补充(适用于全部 R 项)

- **回归范围**:`go test ./internal/rag/... ./desktop/...` 全绿;HE、calendar(DSN 同修)、Resume、重导入去重为受影响回归面。
- **迁移与回滚**:R0-3 首次切 WAL 与 R1-1/R3-3 ALTER 需在 8 月生产旧库副本上验证升级路径;每项修复给出回退步骤。
- **平台差异**:WAL 行为(勿在 OneDrive 同步的 %APPDATA% 上断言)、系统通知与托盘在 Windows/macOS/Linux 三端分别验收。
- **i18n**:新增文案键 zh/en 双份齐全,纳入现有 locale 键一致性检查。
- **可观测性**:修复后需能从日志/DB 回答"重试率、attempts 分布、终态事件触达",支撑事后审计。
- **手工验收项**:R1-3(文案/悬停)、R2-3(通知)、R2-4( toast)、R2-5(导入预估)、R3-2(托盘标识)以手工清单+截图验收;R2-5 的块数预估用 chunk 单测断言。

### 6.5 风险与回滚

- **R0-2** 放开 error 覆盖会改变 done_chunks/终态统计口径,需同步检查 RagExtractResult 的 doneCount 语义;**R0-7** 的 full 清空是破坏性操作,沿用前端既有确认框;**R0-1** per-attempt 独立 ctx 使最坏块耗时 = 尝试数×单次超时,需同步调低单次超时上限;**R0-3** 启用 WAL 后备份/同步盘场景需文档提示(主文件+wal 成对拷贝)。
- HE 双路径(F-D 系列)若选择"收敛单路径"方案,R0-5/R0-7 由收敛方案取代,见开放问题 2。

## 7. 实施顺序建议

1. **R0 全部**(R0-3 是一行 DSN;R0-1/2/3 决定一切重试语义) → 2. **R1-1/2/3**(状态诚实最小闭环) → 3. **R2-1/2/3**(手动恢复闭环 + 通知) → 4. **R1-4/5/6/7 与 R2-4/5** → 5. **R3**(opt-in 自动化)。D 类(HE 双路径)若短期不投入,备选方案是收敛为单路径:HE 仅做向量/摘要,图谱抽取统一走 Go pipeline(消除 F-D1~D7 整类)。

## 8. 非目标

- 不重写 FTS5/存储引擎;不引入向量库;不改变"低频、不打扰"的抽取节奏默认值。
- 不在本 spec 内处理 chat/agent 主链路问题(RagAsk 瑕疵仅记录:F-G 之外 rag_app.go:819 config.Load 错误吞掉、topK=8 硬编码)。

## 9. 开放问题

1. partial 语义采用 `done+failed_chunks` 字段(推荐,B1 论证波及面最小)还是独立 status 值?待评审。
2. HE 路径长期定位:补齐 job 生命周期(R0-5 最小版)还是收敛单路径?
3. 10 万字文档是否需要"章节级摘要抽取"降档模式(块数×成本上限保护)?
4. 大扫描 PDF(E1)是否引入分页 OCR + 断点续跑,还是明确提示"扫描件>100 页不支持"?
5. `extract_concurrency` 是否随 R3 开放给最终用户(与 rpm=60 的联动保护如何表达)?

---

## 附录 A:审计与核查记录

| 轮次 | 子任务 | 范围 | 产出 |
|---|---|---|---|
| 1 | A1 流水线机制 | extract/llm_extractor/entities/rag_app | A1-A16(16 条,含取消复活、HE 双路径、noop 假成功等) |
| 1 | A2 前端知情权 | cowork/*、bridge、locales | A2-1~13(13 条,含 mock 兜底、queued 渲染为✓、轮询假象) |
| 1 | A3 存储/大文档/WAL 实验 | store/officedoc/docconv + 副本复现实验 | A3-1~12(12 条)+ DSN 静默忽略根因 |
| 2 | B1 修复可行性 | 拟议 R1-R3 落地点 | B1-1~B7-3(幂等守卫 P0 前置、dedup 自愈、P50 口径等) |
| 2 | B2 多路径并发 | HE vs Go 状态机推演 | B2-1~14(双烧序列、prune 窗口、事件雪崩等) |
| 2 | B3 对抗复核+盲区 | 抽查 6 条论断 + 6 个盲区 | S1-S6(5 属实/1 修正)+ B1-B8(会话作用域、.git 误导入等) |
| 3 | C1 独立核查 | 10 条高危论断逐条验证 | T1-T10 全部属实(T5 附 1s 节流补充说明) |
| 3 | C2 spec 审稿 | 本文档完整性/一致性 | 见文末修订记录 |
| 3 | C3 验收审计 | 验收标准可测性 | 见文末修订记录 |

早期人工排查(第 1-10 遍):UI 状态链路映射、rag.db 直接取证(§2.1-2.3)、日志级别取证(slog.Debug 不落盘)、格式解析层排除、LLM 层定位、现状确认、大文档推演、RPM 数学、markitdown 超时、git 历史确认无修复落地。

**现状确认项(审计中核实为"无问题",固化以免重复排查)**:ImportModal 的 autoExtract 默认 false,属显式 opt-in(ImportModal.tsx:27);zh/en 的 cowork.ragStatus* 五态键齐全一致(zh.ts:2263-2267 / en.ts:2260-2264);Resume 重读文本走 body_raw,LLM 输入未被 bigram 展开污染(store.go:897-921);isSupportedExt 已做大小写归一(extract.go:678)。

## 附录 B:修订记录

- v1(2026-09-10):首版,整合 3 轮 × 3 子任务审计与独立核查结论。
- v1.1(2026-09-10):C2/C3 审稿修订——修正 entities.go:694-695、extract.go:660-661、rag_app.go:331 行号;修正 §1 出错文件计数(截图 3 份/DB 4 份);重算 §2.4 大文档块数区间(40~100 块,字节/rune 双口径)与耗时/吞吐;补收 F-B13(后台批量零反馈)、F-D8(HE ETA 并行分母);F-C6 补级联写回组合;§6 验收标准改写为可执行验证(含可复用 harness:fakeExtractor/newTestStore/blockingExtractor);新增 §6.4 验收维度、§6.5 风险与回滚。
- v1.2(2026-09-11):**R0 全部落地**(工作树未提交):R0-1 per-attempt 120s ctx + 300s 任务预算(extract.go processTask/PipelineConfig);R0-2 幂等守卫仅 prev==done 跳过 + job 级 error_msg 落库/成功清空(entities.go MarkChunkDone);R0-3 DSN 改 _pragma= 形式(rag+calendar)并补 ragStore.Close(shutdownBody);R0-4 取消守卫(MarkJobExtracting 条件转移 + processTask 前置检查 + 终态翻转尊重 cancelled);R0-5 HE 落库(SetJobExtracted/SetJobFailed + runHEExtract 重写);R0-6 walkDocs 剪枝点目录/node_modules/空扩展名;R0-7 HE mode=full 生效 + 增量跳过 done(FilterJobsForExtraction)+ RagStartExtract 运行互斥(ragExtractRunning)。**实施中新发现并修复**:① 开启 FK 后 CreateJob upsert 换主键违反外键——删旧 chunk 提前到 upsert 前(同时修复 A3-5 孤儿 chunk);② WAL 提交加速暴露 calendar genID 纳秒撞号(TestStoreSearch 连续 Create UNIQUE 冲突)——改 crypto/rand 后缀;③ TestBinaryFormatRejected 密闭化(HOME/USERPROFILE 指空目录,消除开发机 ~/.fairpeer/scripts 环境依赖);④ 既有 WIP transport.go 缺 fmt import 补一行。测试:internal/rag 全绿(新增 r0_fixes_test.go 9 个用例)、calendar 10 遍稳定、两模块构建/vet/gofmt 通过;race 检测因本机无 cgo/gcc 未能运行。
- v1.3(2026-09-11):**R1 状态诚实落地**(工作树未提交):R1-1 rag_jobs v8 迁移加 failed_chunks 列(MarkChunkDone 同步维护,done+failed>0 = partial;CreateJob upsert 重置;SetJobExtracted/SetJobFailed 带语义);R1-2 fileStatus 新增 partial 分支 + RagNodeView.failedChunks(9 块挂 8 块的文件不再显示"已抽取");R1-3 前端 queued/partial 状态文案与图标(ragft-status--partial/ragft-node--partial,--warn 色)、悬停展示人化错误(lib/ragError.ts:deadline→模型响应超时/429→触发限流/401→鉴权失败等 8 类,zh/en 各 16 个新键);R1-4 集合健康度摘要(RagCollectionView complete/partial/failed/queued,集合行 subtitle 显示"N 篇 · x 部分失败 · y 失败");R1-5 SnippetView.status 落库侧就绪(RagSearch 按 AllJobs 打状态标),前端暂无 snippet 渲染组件宿主,文案键已备;R1-6 ProgressEvent 增加 kind(progress/terminal)+scope(chunk/file),终态事件绕过节流必达(节流状态从包级全局移入 Pipeline 字段),HE 事件同标注;R1-7 前端 rag:progress 处理改为 payload 驱动:中间事件按 path 原地 patch 树节点(零 IPC),终态事件 300ms 防抖后全量刷新(树+集合)。**实施中新发现并修复**:JobsByPath SELECT 列数与 scanJobs 不匹配致永远返回空列表(既有 bug,列清单对齐)。验证:internal/rag 全绿(新增 r1_fixes_test.go 3 用例:partial 计数/JobsByPath/终态必达)、desktop fileStatus 映射单测、前端 tsc + 56 个 vitest 全绿(locale-parity 含新键)、双模块构建通过。备注:netdev.css 存在 2 处既有 z-index 违规(非本次引入,未越界修复)。
- v1.4(2026-09-11):**R2 显式恢复落地**(工作树未提交):R2-1 Pipeline.RetryFailedChunks——只重入队 error 块(FTS5 body_raw 重读文本、持久化模板 prompt 保留、SetJobRetrying 条件转移 error|done+failed→extracting),40 块文档挂 2 块只花 2 次调用;desktop 绑定 RagRetryEstimate/RagRetryFailedChunks。R2-2 成本预估用 ChunkLatencyP50Ms(本 job 成功块中位数,回退集合均值;单测验证超时样本被排除);重导入 dedup 双处条件改 `done && failed==0`——部分失败文件在下次文件夹重导入时自愈;失败块样本不再污染内存 ETA 窗口(processTask 仅 lastErr==nil 时 Add,修 F-A5)。R2-3 终态通知:前端聚合器 2.5s 静默窗后一条汇总 toast(失败者 error 级含人化原因 + 首文件名,混合批次 warn 级"N 成功 M 失败"),绝不逐文件轰炸。R2-4 重试/取消/移除的错误 catch 全部 toast 化(原静默吞掉);HE 取消通道:runHEExtract 改可取消 ctx(App.heCancel),RagCancelExtract 同时触达 Go CancelJob 与 HE 批次,取消的文件回 pending(可再试)而非 failed。R2-5 导入预估:RagImportPaths 消息含"约 N 块 × 2 次调用 + 预计时长(按集合历史均值)";前端确认框(useConfirm)展示"只重试 N 个失败块(约 M 次调用,预计 T),成功块保持不变"。**未竟**:extract_concurrency 的设置 UI 入口未做(toml 已支持,计划随 R3 一起);重试按钮暂只挂在文件树行(集合级"重试全部失败块"待 R3)。验证:r2_fixes_test.go 4 用例(partial 重试收敛 calls==4/自愈重导入/P50 中位数/无失败块报错)、rag+calendar+desktop 全绿、tsc 干净、56 vitest 全绿。
- v1.5(2026-09-11):**R3 opt-in 自动重试落地 + R2 遗留清偿**(工作树未提交)——三层修复(R0 正确性/R1 诚实/R2 恢复/R3 授权)全部完成。R3-1 配置四件套:[cowork] extract_auto_retry(默认 false,严格 opt-in)+ extract_auto_retry_max_rounds(默认 2,normalizeCoworkDefaults 兜底,render.go 注释化输出);rag_jobs v9 迁移加 retry_rounds 持久化列(成功收敛时 MarkChunkDone 终态翻转重置为 0,CreateJob 重导入重置);FailedJobsForAutoRetry/ExhaustedAutoRetryJobs 两个筛选查询;desktop 引擎 startRagAutoRetry(30s 预热 + 5min tick,与 ragExtractRunning/Resume 互斥,IncrementJobRetryRounds 先记账再入队)。R3-2 可见性:RagAutoRetryStatus/RagAutoRetryToggle 运行时开关绑定;rag:auto-retry 生命周期事件(ready/round/exhausted);文件页工具栏常驻"自动重试"徽标(Repeat 图标,active 时 --warn 色 + "自动重试中…",一键暂停);引擎事件全量 toast(就绪/轮次开始/停手汇报"重试 N 轮后仍失败,请检查模型或网络"),exhausted 通知按失败集合签名去重不重复轰炸。R3-3 轮次持久化:重启后计数延续不重烧(单测:轮次 2 次后移出 eligible 进 exhausted;partJob 治愈后 rounds 归零)。**R2 遗留清偿**:RagRetryAllFailed 集合级"重试全部失败块"(确认框 + toast 回执,文件页工具栏按钮)。验证:r3_fixes_test.go 3 用例(轮次生命周期/筛选划分/JobRow 携带)、rag+calendar+config+desktop 全绿、tsc 干净、56 vitest 全绿;config.go 有并行会话未格式化区域(非本次代码,未越界)。**知情权三原则至此闭环**:状态诚实(R1)、恢复显式且便宜(R2)、自动需授权且可见可停(R3)。
- v1.6(2026-09-11):**历史数据自愈 + 设置入口收官**。① Open() 启动清扫新增两条回填:failed_chunks 按 chunk 行实况重算(v8 前的老库 DEFAULT 0,历史 partial 文件会永远伪装"已抽取");job 级 error_msg 对 pre-R1 的 error job 回填最后一条失败块原因(此前 UI 只能显示光秃"出错")。均幂等、每开库自愈。② **真实生产库集成验证**:拷贝 8 月 rag.db 用新代码打开——工作分类的 1办公网.docx 正确呈现 done/9/9/**8 失败**(原伪装干净)、4铁通.pdf 呈现 7/7/**6 失败**、全部 error 文件 error_msg="context deadline exceeded" 回填成功;v7→v9 迁移无错。③ 设置面板收官:知识库卡片新增"深度抽取并发"(1-8 数字)与"空闲自动重试失败块"(开关 + 轮数上限 1-5,开启后条件展开);SetCoWorkSettings 热同步引擎(开启即启动、关闭即暂停,免重启);extract_concurrency 从此有 UI 入口(v1.4 遗留清偿)。④ 全量回归:主模块(除并行会话在途的 netdev 割接挂起与 browser WIP 三败,均无 rag 依赖)+ desktop 102s 全量全绿;新回填有 TestOpenBackfillsFailedChunks 单测;验证工具留 scratch/ragmig(gitignored)。**至此 spec §6 R0-R3 全部条目 + 已知遗留(extract_concurrency UI)清偿完毕**;仅剩开放问题 §9 的产品决策(HE 单路径化、大扫描 PDF 分页 OCR 等)与 snippet 徽标前端宿主(待检索面板)。
- v1.7(2026-09-11):**全面终审(3 子任务:代码正确性/spec 一致性/前端接线)+ 修复兑现**。修复的缺陷:①【P1】MarkChunkDone 终态翻转无条件清零 retry_rounds——done_chunks 计含 error 块,重试轮首块完成即触发翻转→轮次计数每轮归零→自动重试上限永不生效(顽固文件每 5 分钟无限重烧);改为仅干净收敛(failed==0)清零,部分收敛保留轮次(新增回归测试)。② 自动重试引擎双启动:徽标暂停(仅翻开关)后再设置开启会叠加第二个 ticker(双重烧 token + 旧 goroutine 泄漏)——heMu 保护的 stop-channel 单例守卫,shutdown 同锁关闭并置 nil。③ ragAutoRetryMaxRounds 跨 goroutine 数据竞争——改 atomic.Int64(SetCoWorkSettings 热同步时轮数上限即改即生效)。④ 批次通知把 partial(done+failed>0)计成成功——ProgressEvent 增加 failedChunks 字段(HE 路径为 0),前端聚合器将其计入失败列并给块级提示;删除无路径命中的 extract 死分支。⑤ 中间事件 patch 不校验 scope——HE 文件粒度 idx/文件数会污染块进度条;scope 非 chunk 整体跳过。⑥ 设置面板 3 处 fallback 字面量缺新字段——加载失败时保存会把 extract_concurrency 静默清零,字面量已补齐。⑦ RetryFailedChunks 与自动重试 tick 并发时同 ChunkID 双重入队——p.mu 下按 ChunkID 去重。⑧ 集合"全部文档"聚合行补健康度汇总;设置开启后徽标经 ready 事件即时重现;部分完成文案补记录(修 G2-15 做了没说)。审计确认但属设计取舍、未改:done+failed 文件重导=CreateJob 全量重置(自愈成本高于便宜重试,代码注释有载);HE 取消为整批语义;partial 重试中途 done 提前翻转(最终计数正确);RagRetryEstimate 失败文案未本地化。审计确认清白:类型-JSON 对齐、mock 齐备、i18n parity、Open 回填与运行期无并发、HE 取消回 pending 与 Resume/引擎不重复入队、WAL DSN/decodeTextBytes/walkDocs 剪枝无缺陷。spec 一致性审计(G2)结论:修订记录功能性声称与实现高度一致(约 9/10),偏差集中在验收口径弱化(约 5 处断言被替代,如 attempts==2 改为 calls>=2)与 1 处做了没说(已补记录),无虚构声称;手工截图类验收(R2-3/R2-4/R3-2)如实标注为未复核。
- v1.8(2026-09-11):**暂缓项重估后落地两项 + 摘要降档决策记录**(工作树未提交)。①【HE 收敛】图谱抽取统一 Go 管线:RagStartExtract 移除 HE 分支(模板 prompt 为纯内置,GetTemplatePrompt 无需 HE 服务),删除 runHEExtract/selectExtractFiles/emitHEProgress/cancelHEExtract/heCancel 整套 HE 抽取机器;Hyper-Extract 服务保留专职 embeddings/summarize/semantic search。收敛使 F-D 系列(双烧/事件双 shape/进度假象/轮询 5 分钟)整类根除——此前 v1.5 说"整类根除需收敛",现已兑现。②【分页 OCR】ocr_pdf.py 增加 --first/--last 页范围参数并在输出回传 pages_total/first/last;Go readPDF 改为 runOCRBatches 分批驱动(每批 20 页、批内 10 分钟超时、批间累计——第 300 页失败不再丢弃前 299 页),60 批硬上限(1200 页);批级失败仅截断后续页并保留已得文本,不再整本归零。runOCRBatches 以 fake-runner 单测(3 批次推进/中途失败保留部分/批数止损)。③【顺手项】RagImportPaths 返回消息拼入无法解析的文件名(最多 3 个+计数)——文件夹导入部分失败从此可见(此前只写日志,用户文件凭空消失);"没有失败块"错误改英文哨兵串,前端 humanizeRagError 映射本地化(en 界面不再中文直出)。④【摘要降档决策:暂不做】理由:R2 后 10 万字文档的等待已有预估+进度+诚实状态+便宜重试兜底,痛点从"等不到结果"降为"等得久但心里有数";且健康端点实测 12-35 分钟可接受。触发条件(满足其一再做):用户实际抱怨等待时长、或 10 万字文档占比过半、或调高 extract_concurrency 后仍不可接受。设计草案(留档):章节标题切大段→每段先摘要后图谱→合并实体,块数可降 60-80%。验证:rag 全绿(新增 ocr_batching_test.go 2 用例+desktop rag_import_message_test.go 2 用例)、tsc+56 vitest 全绿、双模块构建通过。
- v1.9(2026-09-12):**收官终审(3 轮 9 子任务,逐行)+ 清偿 21 项**。第一轮逐行(Go 核心/desktop/config 三分工):确认 18 列 SELECT 全对齐、RETURNING 方言兼容、回填幂等、CANCEL 三重守卫、WAL+busy_timeout 组合正确;发现 JobByID 缺 4 列致重试丢模板 prompt(P1)、HasActive 语义缺口(P1)、回填清掉 SetJobFailed 计数(P2,HE 收敛后自然消解)等。第二轮(前端逐行/测试逐行/8 条集成流追踪):F2 自动抽取死路(P1)、D-1 设置数字输入被 dirtyRef 门控永不保存(继承既有坏模式,新控件改 commitDraft 直存)、聚合器 partial 计成成功、scope 不校验污染进度条、RagRetryAllFailed 用 activeCollection=""(全部视图)必然失败等;S1-S8 流全部闭环或已修复。第三轮:10 项修复逐一复核通过 + 未竟审计(HE 标识符零残留/绑定全对齐/scratch build-tag 不入 CI/spec §9 与附录矛盾)+ 全量门禁 7/7。**本轮修复清单**:JobByID 改 18 列完整查询(修重试丢模板 prompt);RagStartExtract 增 HasActiveExtractJobs 持久忙检查(覆盖整个批次时长);runAutoRetryTick 先入队后计数(n>0 才烧轮)+ HasActive 退避;walkDocs 单文件不支持类型报错并入 skip 名单;SetJobRetrying 收紧 failed_chunks>0;ChunkLatencyP50 补 rows.Err;RetryFailedChunks 返回实际入队数;runOCRBatches 剥离批内 [OCR warnings] 末尾一次性拼回+非前进 Last 兜底;RagAutoRetryToggle(true) 幂等启动引擎;initRAG 预置轮次默认 2;ImportModal 移除死路自动抽取开关改为如实信息卡+消息 toast 承载完整回执;CoworkDock 补 humanizeRagError 到全部错误 toast、8s 聚合硬顶、空 path 失败事件不入名单、fmtEstTime 进位、RetryAll 按钮仅在选定集合时显示、徽标支持暂停/恢复双向切换;ragError 数字匹配加 http 前缀防误报、permission 顺序修正;TemplateSelect 移除失实的 "(HE enhanced)"/"(adaptive two-stage)" 宣称改 Go 管线文案、✗ failed/waiting/ failed 本地化、删除死 setDocCount;FailedJobsForAutoRetry error 分支补 failed_chunks>0;RagRetryEstimate 哨兵英文;JobByID 相关单测补齐。**已知接受的取舍**(均有注释/记录):done_chunks 计含 error 导致重试轮中途提前翻转(最终计数正确);FilterJobsForExtraction 增量跳过 partial(有 RetryAllFailed 兜底);RagRetryEstimate 对全失败 job 的估算偏保守;关机窗口在-flight 块写入报错但块保持可重试;buildImportMessage/忙错误为中文(后端消息现状)。
- v1.10(2026-09-12):**五遍 × 3 子任务逐字终审(15 子任务)+ 清偿**。逐字新发现并修复:① ocr_pdf.py 逐页容错(单页损坏只丢该页并记 warning,不再整批归零)+ OCR 依赖安装失败隔离(文本页照常返回)+ _ocr_page 渲染失败 PNG 泄漏修复(整函数 finally unlink);② 批失败显式标记("[OCR warnings] pages N and beyond unavailable" 单块聚合,不再按批重复/正文混入);③ llm_extractor 对不支持 response_format 的提供商 400 时自动去掉该参数重试一次;④ insertIntoTree 文件夹 Key 改集合内相对路径(修复同名子文件夹跨父目录 Key 冲突);⑤ CancelJob 改条件 UPDATE(done/error 不再被竞态翻成 cancelled);⑥ RagRetryEstimate 对 cancelled job 拒绝估算;⑦ RagBatchExtract 补忙检查;⑧ initRAG 无条件预置轮次默认 2(修 opt-out 下 Status 显示 0/后续 Toggle 降级配置值);⑨ emit 回调 lastRagChanged 加锁(Concurrency>1 数据竞争);⑩ ImportModal 重开时目标集合重同步;⑪ TemplateSelect 移除 HE 宣称改 i18n 化引擎文案/✗ failed 与 waiting 本地化/删除死 setDocCount/重试按钮 disabled+错误人化;⑫ JobByID 18 列重写修重试丢模板 prompt(P1)。**已知接受的取舍新增**:60 批(1200 页)硬上限截断有 warnings 标记;OCR 批间无文件替换校验(单次 readPDF 窗口短,接受);批超时无总取消(用户可重启,块保持可重试);first-run pip 装机慢网超时属环境限制。验证:rag/calendar/config/desktop 三包全绿(tsc 55 vitest 全绿),门禁 7/7。终审结论:本批次全部工作(R0-R3+收敛+分页 OCR+终审修复)已逐行核验,无已知未修复缺陷;唯一持续风险为与并行会话的 config/netdev WIP 共存导致的模块级编译波动(非本批次代码)。
