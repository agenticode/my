// Command my is the MemorY command line tool.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/agenticode/my/internal/cli"
)

func app() *cli.App {
	return &cli.App{
		Name:             "my",
		Synopsis:         "MemorY reads the memory error records of a Linux server and shows which DIMM is failing, how, and what it rests on.\nIt never changes system state.",
		SynopsisKO:       "MemorY는 Linux 서버의 메모리 오류 기록을 읽어 어느 DIMM이 어떤 고장 모드로 실패하는지, 그 근거가 무엇인지 보여줍니다.\n시스템 상태를 바꾸지 않습니다.",
		DefaultSummary:   "the same as \"my scan\"",
		DefaultSummaryKO: "\"my scan\"과 같음",
		Default:          &cli.Command{Name: "scan", Run: live(runOverview)},
		Commands: []*cli.Command{
			{
				Name:      "scan",
				Summary:   "scan this host: slot map and DIMMs ranked by risk",
				SummaryKO: "이 호스트를 스캔: 슬롯 맵과 위험 순위",
				Run:       live(runOverview),
			},
			{
				Name:      "dimm",
				Summary:   "examine one DIMM: finding, topology, address map, timeline",
				SummaryKO: "DIMM 하나의 진찰 화면: 판정 카드, 토폴로지, 주소 맵, 타임라인",
				Run:       live(runDIMM),
			},
			{
				Name:      "why",
				Summary:   "the finding and what it rests on; --oneline for a ticket",
				SummaryKO: "판정 근거: 규칙, 기록, 반례, 부족한 데이터. --oneline은 티켓용 한 줄",
				Run:       live(runWhy),
			},
			{
				Name:      "map",
				Summary:   "address map of one bank: errors by row and column",
				SummaryKO: "bank 하나의 주소 맵: row x column",
				Run:       live(runMap),
			},
			{
				Name:      "timeline",
				Summary:   "errors per day with boots, repairs and uncorrected errors",
				SummaryKO: "일별 오류와 부팅, 리페어, UE",
				Run:       live(runTimeline),
			},
			{
				Name:      "gpu",
				Summary:   "GPU memory as a repair budget: Xid records, remapped rows, spare rows per bank",
				SummaryKO: "GPU 메모리를 리페어 예산으로: Xid 기록, remap된 row, bank별 spare row",
				Run:       live(runGPU),
			},
			{
				Name:      "bundle",
				Summary:   "write the records of this host as one case file to send or attach",
				SummaryKO: "이 호스트의 기록을 보내거나 첨부할 케이스 파일 하나로 저장",
				Run:       live(runBundle),
			},
			{
				Name:      "report",
				Summary:   "write the findings of a case as one HTML file that prints to PDF",
				SummaryKO: "케이스의 소견을 HTML 파일 하나로 저장 (인쇄하면 PDF)",
				Run:       live(runReport),
			},
			{
				Name:      "export",
				Summary:   "write the findings as an FMFM analysis log (JSON): my export fmfm",
				SummaryKO: "소견을 FMFM 분석 로그(JSON)로 내보내기: my export fmfm",
				Run:       live(runExport),
			},
			{
				Name:      "open",
				Summary:   "show a stored case (a .mycase bundle or a ledger) with the same commands",
				SummaryKO: "저장된 케이스(.mycase 번들 또는 원장)를 같은 명령으로 보기",
				Run:       runOpen,
			},
			{
				Name:      "diff",
				Summary:   "compare two cases of one host: before and after a reseat, a slot swap, a firmware update",
				SummaryKO: "한 호스트의 케이스 둘을 비교: reseat, 슬롯 맞교환, 펌웨어 업데이트의 전후",
				Run:       runDiff,
			},
			{
				Name:      "import",
				Summary:   "build a case from files: kernel logs, a rasdaemon database, BMC dumps, a copy of a host",
				SummaryKO: "파일로 케이스 만들기: 커널 로그, rasdaemon DB, BMC 덤프, 호스트 파일 사본",
				Run:       runImport,
			},
			{
				Name:      "serve",
				Summary:   "run as a server: Prometheus metrics and the findings of this host over HTTP",
				SummaryKO: "서버로 실행: 이 호스트의 Prometheus 지표와 소견을 HTTP로 제공",
				Run:       runServe,
			},
			{
				Name:      "verify",
				Summary:   "compare an error injection plan with what the host recorded",
				SummaryKO: "에러 주입 계획과 호스트의 기록 대조",
				Run:       live(runVerify),
			},
			{
				Name:      "version",
				Summary:   "print version and build information",
				SummaryKO: "버전과 빌드 정보",
				Run:       runVersion,
			},
			topicCommand("reading", printReading),
			topicCommand("rules", printRuleList),
			topicCommand("thresholds", printThresholdList),
			synthCommand(),
			analyzeCommand(),
			evalCommand(),
		},
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app().Run(ctx, cli.OSEnv(), os.Args[1:])
	stop()
	os.Exit(code)
}
