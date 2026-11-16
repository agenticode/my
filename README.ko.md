# MemorY

[English](README.md) | [한국어](README.ko.md)

MemorY는 Linux 서버가 이미 남기고 있는 메모리 오류 기록을 읽어서 네 가지를 보여줍니다. 어느 DIMM이 고장 나고 있는지, 고장이 DIMM 안 어디에 있는지, 재부팅과 리페어를 거치며 어떻게 변했는지, 그 소견이 어떤 기록에 근거하는지입니다.

메모리 오류를 읽고 조치를 정해야 하는 사람이 쓰는 도구입니다. 플랫폼과 데이터센터 엔지니어, SRE, 하드웨어 벤더의 필드와 지원 엔지니어, 시스템 통합 업체, 커널과 RAS 개발자가 여기에 듭니다.

명령은 `my`입니다. 설정 없이 실행되는 정적 바이너리 하나이고 읽기만 합니다. 페이지를 offline하지 않고, 리페어를 시작하지 않고, 카운터를 초기화하지 않습니다.

![예제 호스트의 슬롯 맵: CPU0_C1은 즉시 교체, CPU1_D1은 관찰](examples/ko/figures/slotmap.svg)

![CPU0_C1의 주소 맵: row 0x1a2f에 오류 칸이 가로로 늘어서 있고 그중 하나가 UE](examples/ko/figures/CPU0_C1-map.svg)

## 필요한 이유

서버는 메모리 오류를 여러 곳에 기록합니다. rasdaemon 데이터베이스, 커널 로그, EDAC 카운터, BMC 이벤트 로그가 그렇고 형식이 제각각입니다. 카운터는 부팅할 때마다 0에서 시작하고 로그는 순환됩니다. 기록 한 건은 오류 한 건의 위치를 알려 줍니다. 한 DIMM의 오류가 모여 이루는 고장은 어느 기록에도 없습니다.

그래서 사람이 정해야 할 것이 그대로 남습니다. 어느 DIMM인지, 셀 하나인지 row인지 디바이스 전체인지, DIMM 문제인지 슬롯 문제인지, 나빠지고 있는지, 리페어가 효과가 있었는지입니다.

MemorY는 모든 소스의 기록을 DIMM 시리얼을 키로 하는 원장 하나에 모읍니다. DIMM의 이력은 재부팅 뒤에도 남고, DIMM을 다른 슬롯으로 옮기면 따라갑니다. MemorY는 그 이력을 그림으로 그리고 DIMM마다 소견을 냅니다. 소견에는 판정한 규칙, 근거가 된 기록, 다른 원인을 따져 본 반례 점검, 확인하지 못한 데이터가 함께 나옵니다.

## 화면

아래 화면은 이 저장소에 든 예제 케이스에서 나온 것입니다. 7주 동안 row 고장이 커진 합성 호스트입니다([examples/](examples/README.md)). 서버에서 인자 없이 `my`를 실행하면 호스트를 스캔해 같은 개요를 그립니다.

```
$ my open examples/demo.mycase
2026-11-13 09:30 UTC 케이스 번들, 작성 도구 my dev; 대상 CPU0_C1 S/N 3B9C56
호스트 r12-n034  Example Systems R760  2026-07-31 ~ 2026-11-13  CE 1,284건, UE 1건  관측 등급 A
호스트 소견: 즉시 교체

        A1   B1   C1   D1   E1   F1   G1   H1
 CPU0   ░░   ░░   ██   ░░   ░░   ░░   ░░   ░░
 CPU1   ░░   ░░   ░░   ▒▒   ░░   ░░   ░░   ░░

 ██ 즉시 교체   ▓▓ 교체 고려   ▒▒ 관찰   ░░ 정상   ?? 교체 보류
 슬롯 32개, DIMM 16개 (빈 슬롯 16개는 그리지 않음)

 #  슬롯     시리얼     CE  UE  모드        추세 30일     소견       규칙
 1  CPU0_C1  3B9C56  1,231   1  row         ▃▄▄▄▅▅▆▆▇█ ↑  즉시 교체  R-UE-01 R-ROW-03 R-REP-02
 2  CPU1_D1  2AEB41     53   0  single bit  ▆▁▄█▃▄▃▄▄▁ ↓  관찰       R-SOFT-01

 오류 없는 DIMM 14개
 소스  synth 1,297건

 my open examples/demo.mycase dimm CPU0_C1: 이 DIMM 진찰 화면
 my open examples/demo.mycase why CPU0_C1: 판정 근거   my help: 판독법
```

DIMM 하나를 진찰한 화면입니다. 소견, 오류가 몰린 가지, 오류가 가장 많은 bank의 주소 맵, 재부팅을 가로지르는 타임라인이 차례로 나옵니다.

```
$ my open examples/demo.mycase dimm CPU0_C1
2026-11-13 09:30 UTC 케이스 번들, 작성 도구 my dev; 대상 CPU0_C1 S/N 3B9C56
CPU0_C1  DDR5 RDIMM 64 GB 2Rx4  Example Memory EXM64G48R2X4-A  S/N 3B9C56
이 슬롯 첫 관측 2026-07-31  슬롯 이동 0회

 판정  즉시 교체 (소견, 신뢰도 높음)                                       R-UE-01 R-ROW-03 R-REP-02
 근거  [R-UE-01] UE 1건, 최근 2026-11-12 03:14 UTC, rank0 dev7 bg0 bank3 row 0x1a2f col 0x238
       [R-ROW-03] row 고장(rank0 dev7 bg0 bank3 row 0x1a2f): 최근 7일 421건, 그 앞 30일 일평균의
       2.3배
       [R-ROW-03] row 고장(rank0 dev7 bg0 bank3 row 0x1a2f): 최근 30일 새 위치 13곳, 현재 row 2개에
       column 9개
       [R-ROW-03] rank0 dev7 bg0 bank3 row 0x1a2f 고장의 오류에 한 디바이스의 DQ 2개가 관여
       [R-REP-02] 2026-10-28 hPPR 뒤 rank0 dev7 bg0 bank3 row 0x1a2f에서 764건 재발
 반례  미확인: 이 채널에 다른 DIMM 없음: 비교 대상 없음
       미확인: 슬롯 교차 시험이나 reseat 기록 없음: 보드 원인을 배제하지 못함
       뒷받침: 이 호스트의 다른 DIMM 15개에는 한 위치에 국한되지 않은 오류 없음: 보드 원인 가능성
       낮음
 부족  ECS 카운터 없음
       DIMM 온도 이력 없음
 기준  자체 이력: 최근 7일 하루 60.1건, 그 앞 30일의 100 백분위
       같은 부품번호(EXM64G48R2X4-A): 이 원장의 DIMM 16개 중 97 백분위, 오류 있는 DIMM 2개
 다음  DIMM 교체. 이 DIMM의 케이스 번들을 반품에 첨부.

 오류가 몰린 가지 (폭 = 이 DIMM 오류 중 비중)
 rank    |rank0 ██████████████████████████████████████████████████████████████████████████████ 100%|
 device  |dev7 ███████████████████████████████████████████████████████████████████████████████ 100%|
 bank    |bg0 bank3 ██████████████████████████████████████████████████████████████████████████ 100%|
 row     |0x1a2f ████████████████████████████████████████████████████████████████████████████ 98%|█|

 rank0 dev7 bg0 bank3  row x column  한 칸 = column 32개
 · 0   ░ 1-2   ▒ 3-9   ▓ 10-99   █ 100+   X UE

 row       0x000                                                      0x7ff  (column)
 0x1a2c    ································································
 0x1a2d    ································································
 0x1a2e    ································································
 0x1a2f    ··············▓··X·····█·▓··█····▓························▒···▒·  ← row 고장
 0x1a30    ··············░··▒·····▒·░··▒···································  ← 같은 고장
 0x1a31    ································································
 0x1a32    ································································
 0x1a33    ································································

 row 고장(rank0 dev7 bg0 bank3 row 0x1a2f): 1,232건, row 2개에 column 9개, DQ2, DQ3, 첫 발생
   2026-09-24, 최근 2026-11-12

 일별 오류와 이벤트
           07-31     09-01     10-01     11-12
 CPU0_C1   ▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▂▂▂▂▃▃▃▃▄▄▄▅▅▆▆▇█  최대 하루 73건
 이벤트            B           B        BP   OU
                   │           │        ││   │└ 2026-11-12 UE(demand read)
                   │           │        ││   └─ page offline 7건, 2026-11-11 ~ 2026-11-12
                   │           │        │└───── 2026-10-28 hPPR
                   └───────────┴────────┴────── 재부팅 3회

 2026-07-31 ~ 2026-11-12: CE 1,231건, UE 1건
 그중 2026-10-28 부팅 이후 790건: 부팅마다 0에서 시작하는 카운터에는 이것만 보임
```

주소 맵에서 가로줄은 row 고장, 세로줄은 column 고장, 반복되는 한 칸은 single bit 고장, bank에 퍼진 구름은 bank 고장입니다. 터미널에서 `my help reading`을 실행하면 그림마다 읽는 법이 한국어로 나옵니다. 같은 내용을 자세히 쓴 문서가 [docs/reading-guide.md](docs/reading-guide.md)입니다. `docs/` 아래 문서는 영어입니다.

`my report`는 케이스를 HTML 파일 하나로 저장합니다. 그림은 SVG로 들어가고 DIMM별 소견, 오진 점검표, 원본 기록이 함께 담깁니다. 밖에서 불러오는 것이 없어서 네트워크 없이 열리고, 브라우저에서 인쇄하면 PDF가 됩니다. 예제의 리포트는 [examples/ko/demo-report.html](examples/ko/demo-report.html)이고, 아래는 그 안에 든 `CPU0_C1`의 타임라인입니다.

![CPU0_C1의 타임라인: 9월 말부터 하루 CE가 늘고, 아래에 재부팅, hPPR, page offline, UE가 표시됨](examples/ko/figures/CPU0_C1-timeline.svg)

## 빠른 시작

아직 태그한 버전이 없어서 소스에서 빌드합니다. Go 1.26 이상이 필요합니다.

```
go install github.com/agenticode/my/cmd/my@latest
```

저장소를 체크아웃했다면 `make build`가 `bin/my`를 만들고, `make cross`가 linux/amd64, linux/arm64, darwin/arm64용 바이너리를 만듭니다. 정적 링크 바이너리라서 파일 하나만 서버에 복사하면 됩니다.

서버에서 실행합니다.

```
sudo my                          # 슬롯 맵과 위험 순위. 종료 코드가 곧 등급
sudo my dimm CPU0_C1             # DIMM 하나: 소견, 토폴로지 막대, 주소 맵, 타임라인
sudo my why CPU0_C1 --oneline    # 티켓에 붙일 한 줄 소견
sudo my bundle -o case.mycase    # 스캔이 읽은 것 전부를 파일 하나로
```

root 권한이 없어도 실행됩니다. 그때는 호스트가 열어 둔 것만 읽고, 읽지 못한 소스를 화면에 적습니다. 대부분의 호스트에서 DIMM 시리얼이 든 SMBIOS 테이블을 읽지 못하고, 호스트에 따라 커널 로그나 rasdaemon 데이터베이스도 읽지 못합니다.

받은 케이스는 Linux나 macOS 어디서든 엽니다.

```
my open case.mycase                     # 보낸 사람이 본 화면
my report case.mycase -o report.html    # 같은 케이스를 문서로
```

서버가 없으면 체크아웃한 저장소에서 `my open examples/demo.mycase`를 실행해 위 예제를 봅니다.

## 수집 도구, 리페어 도구와 다른 점

MemorY는 오류를 수집하지 않고 조치도 하지 않습니다. 그 일을 하는 도구가 남긴 기록을 읽습니다.

| 도구 | 하는 일 | MemorY가 하는 일 |
| --- | --- | --- |
| rasdaemon, mcelog | 오류 이벤트와 건수를 기록 | rasdaemon 데이터베이스, MySQL이나 PostgreSQL 서버에 둔 저장소, mcelog 데몬의 건수를 읽음 |
| 커널: EDAC 드라이버, 펌웨어 오류 기록, machine check | 오류를 감지하고 디코딩해 로그에 남김 | 커널 로그와 EDAC 카운터를 읽음 |
| 커널의 page offline, 펌웨어의 PPR과 sparing, BMC의 리페어 서비스 | 페이지를 빼고, row를 고치고, 디바이스를 예비로 돌림 | 언제 무엇을 했고 그 뒤 오류가 재발했는지 타임라인에 표시. 직접 실행하는 것은 없음 |
| BMC | 운영체제 밖에서 이벤트 로그를 보관 | `ipmitool sel` 출력과 Redfish 덤프를 읽음. `--bmc`와 `my collect redfish`는 Redfish 서비스에 직접 묻고 아무것도 바꾸지 않음 |
| 메모리 벤더의 고장 분석기(OCP FMFM) | 디바이스 지식으로 고장을 분류 | 기록을 FMFM 필드 이름으로 넘기고 답을 표시(`--plugin`) |
| Prometheus | 시계열을 저장하고 알림을 보냄 | `my serve --metrics`가 DIMM별 소견을 내보냄 |

## 입력과 관측 등급

| 입력 | 읽는 곳 | 얻는 것 |
| --- | --- | --- |
| rasdaemon | `/var/lib/rasdaemon/ras-mc_event.db`. `--fleet`을 쓰면 MySQL, MariaDB, PostgreSQL에 둔 저장소 | EDAC 드라이버가 디코딩한 위치가 든 오류 기록, machine check, page offline, CXL 이벤트 |
| 커널 로그 | 호스트에서는 저널과 `/dev/kmsg`. 파일로는 `dmesg` 출력, syslog, `journalctl -o json` | EDAC 줄, 펌웨어 오류 기록, machine check, page offline, 부팅, NVIDIA 드라이버의 Xid 줄 |
| 크래시 로그 | `/sys/fs/pstore`, `/var/lib/systemd/pstore`, `/var/crash` | panic으로 끝난 부팅의 커널 로그: reset 전에 출력한 기록 |
| EDAC 카운터 | `/sys/devices/system/edac/mc` | 부팅 이후 DIMM별 건수 |
| mcelog 데몬 | `mcelog --client` | DIMM별 건수 |
| SMBIOS | `/sys/firmware/dmi/tables` 또는 `dmidecode` 출력 | 슬롯, DIMM 시리얼과 부품번호 |
| BMC | `ipmitool sel` 출력, Redfish 덤프, Redfish 서비스 | 메모리 이벤트, 펌웨어가 비활성화한 모듈, sparing |
| GPU 메모리 | `nvidia-smi -q`와 쿼리 출력 | GPU별 row remapping 상태와 ECC 카운터 |
| FMFM 로그 | JSON 파일 | 런타임, 부팅, 크래시 기록과 ECS 값 |
| 호스트 파일 사본 | 아래에 `proc`, `sys`, `var`가 있는 디렉터리(`--root`, `my import`) | 위 입력을 파일로 |

파일은 내용으로 종류를 알아내므로 `my import`에 섞어서 줘도 됩니다. 오류 위치를 얼마나 자세히 알려 주는지는 플랫폼마다 다릅니다. 그래서 기록마다 관측 등급이 붙고, 소견은 기록의 등급이 허락하는 데까지만 말합니다.

| 등급 | 알 수 있는 위치 | 가능한 판단 |
| --- | --- | --- |
| A | bank, row, column | 고장 모드: single bit, row, multi-row, column, bank, device, DIMM |
| B | 물리 주소 또는 rank | 페이지 단위 반복과 추세. row 고장은 추정 |
| C | 모듈, 또는 건수만 | 추세와 임계값 |

입력별 설명과 플랫폼별 등급은 [docs/compatibility.md](docs/compatibility.md)에 있습니다.

## 명령

| 명령 | 하는 일 |
| --- | --- |
| `my` 또는 `my scan` | 이 호스트를 스캔: 슬롯 맵과 위험 순위 |
| `my dimm <슬롯\|시리얼>` | DIMM 하나를 진찰: 소견, 토폴로지 막대, 주소 맵, 타임라인 |
| `my why [슬롯\|시리얼]` | 소견과 그 근거가 된 기록, 반례 점검, 신뢰도. `--oneline`은 티켓용 한 줄 |
| `my map <슬롯\|시리얼>` | bank 하나의 주소 맵. `--bank`, `--rank`, `--device`로 고르고 `--rows A-B`로 확대 |
| `my timeline [슬롯\|시리얼]` | 일별 오류와 부팅, 리페어, page offline, 슬롯 이동, UE |
| `my gpu [gpu]` | GPU 메모리의 리페어 예산: remap한 row, 대기 중이거나 실패한 remapping, bank별 남은 예비 row |
| `my bundle [슬롯\|시리얼] -o case.mycase` | 이 호스트의 기록을 보내거나 첨부할 파일 하나로 저장 |
| `my open FILE [명령 ...]` | 번들이나 원장 파일에 위 명령을 실행 |
| `my report [FILE] -o report.html` | 케이스나 이 호스트를 HTML 파일 하나로 저장. `--audience fa`는 불량 분석에 필요한 내용을 추가 |
| `my diff BEFORE AFTER` | 한 호스트의 두 케이스를 비교. reseat, 슬롯 교차, 교체, 펌웨어 업데이트 전후에 오류가 DIMM을 따라갔는지 슬롯에 남았는지 확인 |
| `my import <파일\|디렉터리>...` | 호스트 없이 파일만으로 케이스를 만듦 |
| `my collect redfish --bmc ADDRESS -o OUT` | BMC에 Redfish로 메모리, BIOS 설정, 로그를 물어 저장 |
| `my inventory` | DIMM을 한 줄에 하나씩: 제조사, 파트 넘버, 용량, 구성, 속도, 적용 중인 RAS 설정, 운영체제와 BMC가 각각 기록한 CE |
| `my bios` | BIOS의 메모리 RAS 설정을 프로필과 대조([docs/redfish.md](docs/redfish.md#bios-memory-ras-settings)) |
| `my export fmfm` | 소견을 OCP FMFM 프레임워크 형식의 분석 로그로 저장([docs/fmfm-mapping.md](docs/fmfm-mapping.md)) |
| `my export labels` | 슬롯 이름을 rasdaemon용 DIMM 라벨 파일로 저장 |
| `my verify --expect plan.yaml` | 오류 주입 계획과 호스트가 기록한 것을 대조([docs/verify.md](docs/verify.md)) |
| `my preflight` | 치명적 메모리 오류 직후 reset이 일어나면 이 호스트에 남는 것과 그것을 바꾸는 설정([docs/crash-loop.md](docs/crash-loop.md)) |
| `my serve --metrics` | Prometheus exporter로 실행([docs/exporter.md](docs/exporter.md)) |
| `my help [명령]` | 명령의 옵션과 출력 읽는 법 |

`--lang en|ko`로 언어를 고릅니다. 기본값은 로캘을 따릅니다. `--json`은 그림에 쓰인 데이터를 출력하고, `--ascii`는 ASCII 문자만으로 그립니다. 등급은 색과 함께 글리프의 농도로도 구분됩니다. 색은 터미널에서만 쓰고 `NO_COLOR`가 있으면 쓰지 않습니다.

```
$ my open examples/demo.mycase why CPU0_C1 --oneline
2026-11-13 09:30 UTC 케이스 번들, 작성 도구 my dev; 대상 CPU0_C1 S/N 3B9C56
r12-n034 CPU0_C1 S/N 3B9C56: 즉시 교체 (소견, 신뢰도 높음; R-UE-01, R-ROW-03, R-REP-02). UE 1건, 최근 2026-11-12 03:14 UTC, rank0 dev7 bg0 bank3 row 0x1a2f col 0x238. DIMM 교체. 이 DIMM의 케이스 번들을 반품에 첨부.
```

## 소견과 종료 코드

소견을 보여주는 명령은 보여준 대상의 등급으로 종료합니다. 대상은 호스트이거나 지정한 DIMM입니다.

| 코드 | 등급 | 조건 |
| --- | --- | --- |
| 0 | 정상 | 오류 기록 없음 |
| 1 | 관찰 | CE가 있고 커지는 고장은 없음 |
| 2 | 교체 고려 | row나 column 고장이 증가하거나 확산, bank 이상을 덮는 고장, 계속 늘어나는 page offline, 패트롤 스크럽이 찾은 UE, 리페어 뒤 재발 |
| 3 | 즉시 교체 | UE 발생, 펌웨어가 failed로 표시한 DIMM, 리페어 자원 소진 |
| 4 | 교체 보류 | DIMM 밖 원인 의심(채널, 슬롯, 접점, 서로 어긋나는 슬롯 이름), 또는 어느 DIMM인지 기록에 없는 UE |

종료 코드 64는 잘못된 명령줄, 70은 실행 실패입니다. 오류 기록 소스를 하나도 읽지 못한 스캔은 "오류 기록 없음"이라고 답하지 않습니다. 찾아본 소스를 나열하고 70으로 끝납니다.

호스트 등급은 DIMM, GPU, 호스트 소견 가운데 가장 나쁜 등급입니다. 호스트 소견은 어느 DIMM의 것인지 기록에 없는 오류와 page offline을 다룹니다. 가상 머신에서는 게스트가 호스트 장비의 오류를 거의 전달받지 못한다는 점을 모든 소견에 적습니다.

등급은 소견입니다. 등급을 가르는 임계값은 기본값입니다. 기본값은 공개 플릿 데이터셋 한 건의 장애 티켓과 대조했고 결과는 [docs/evaluation.md](docs/evaluation.md)에 있습니다. 그 데이터에서 "교체 고려" 서버는 30일 안에 장애 티켓이 난 비율이 2.5%였고, "관찰" 서버의 약 10배입니다. 불량 분석 결과와는 아직 대조하지 못했습니다. 규칙별 조건과 임계값은 [docs/rules.md](docs/rules.md)에 있고, `--set key=value`나 `--config FILE`로 임계값을 바꿉니다.

## 케이스 번들

케이스는 파일 하나로 서버 앞에 있는 사람에게서 판독하는 사람에게 건너갑니다.

1. 서버에서 `sudo my bundle CPU0_C1 -o case.mycase --anonymize`를 실행합니다. DIMM을 지정하면 그 DIMM이 케이스 대상이 됩니다. 번들에는 호스트 전체가 들어갑니다. DIMM 하나의 소견이 이웃 DIMM에도 근거하기 때문입니다.
2. 파일을 보내거나 DIMM 반품에 첨부합니다. `ssh HOST sudo my bundle -o - > case.mycase`는 호스트에 파일을 남기지 않고 가져옵니다.
3. 받은 사람은 `my open case.mycase`로 보낸 사람이 본 화면을 보고, `my report case.mycase -o report.html`로 문서를 만듭니다.

번들은 ZIP 아카이브입니다. 원장, 매니페스트, 소스 인벤토리, 그리고 내용물을 글로 적은 `README.txt`가 들어 있습니다. `my open`은 번들을 바꾸지 않고, 기록에서 소견을 다시 계산합니다. 새 버전은 예전 케이스를 그대로 읽습니다. `my diff before.mycase after.mycase`는 한 호스트의 두 케이스를 비교합니다([docs/diff.md](docs/diff.md)). 형식은 [docs/bundle-format.md](docs/bundle-format.md)에 있습니다.

## BMC

`--bmc ADDRESS`를 주면 명령이 호스트의 BMC에도 Redfish로 묻습니다. 요청은 모두 GET이고, 메모리 모듈과 그 오류 카운트, BIOS 메모리 설정, 이벤트 로그를 읽습니다. `my inventory`는 DIMM을 한 줄에 하나씩 적고 운영체제와 BMC가 각각 기록한 CE를 나란히 놓습니다. `my bios`는 메모리 RAS 설정을 프로필과 대조합니다. 프로필의 속성 이름은 모두 서버 벤더의 공개 문서에서 가져왔습니다. `my export labels`는 BMC의 슬롯 이름을 rasdaemon용 DIMM 라벨 파일로 씁니다. 호스트에 들어갈 수 없으면 `my collect redfish`로 BMC만 읽습니다.

보내는 요청, 인증서와 암호 처리, 아직 시험하지 못한 것은 [docs/redfish.md](docs/redfish.md)에 있습니다. 실제 서버의 BMC는 아직 읽어 보지 못했습니다.

## 프라이버시

MemorY에는 텔레메트리가 없고, 프로젝트가 운영하는 서버에 접속하는 일도 없습니다. 네트워크는 명령줄에서 요청한 세 경우에만 씁니다. `--fleet`은 플릿의 rasdaemon이 기록하는 데이터베이스 서버를 읽고, `--bmc`와 `my collect redfish`는 BMC에 묻고, `my serve`는 Prometheus의 요청을 받습니다. 호스트 밖으로 나가는 것은 사람이 보내는 번들입니다.

| 번들에 담기는 것 | 번들에 담기지 않는 것 |
| --- | --- |
| 메모리 오류 기록: 시각, 심각도, 위치, 그리고 기록을 읽어 온 로그 줄이나 데이터베이스 행 | 메모리, 파일, 프로세스의 내용 |
| 호스트가 기록한 리페어와 page offline | 사용자 계정, 자격 증명, 키 |
| DIMM 신원: 제조사, 부품번호, 시리얼, 용량 | 네트워크 설정 |
| 보드의 슬롯과, 어느 DIMM이 언제 어디에 있었는지 | 메모리 오류, page offline, machine check, 부팅 배너와 무관한 커널 로그 줄 |
| 부팅: 시각, 커널 릴리스, BIOS 버전, 커널 명령줄 | |
| 호스트 이름, 아키텍처, 시스템 제품명 | |
| DIMM별 오류 카운터 값, GPU 메모리 기록, ECS 값, BMC가 보고한 내용 | |
| 찾아본 소스 전부와 읽은 경로, 소스별 결과 | |
| 보낸 사람의 실행에서 나온 소견 | |

`--anonymize`는 호스트 이름, IP 주소, MAC 주소, 커널 명령줄의 디스크 식별자, 파일 경로에 든 홈 디렉터리 이름을 키가 있는 해시로 만든 단어로 바꿉니다. 바꾼 호스트 이름은 보낸 사람이 보관하도록 화면에 출력합니다. DIMM 시리얼은 그대로 둡니다. 반품이 시리얼 단위로 이뤄지기 때문입니다. 보내기 전에 `unzip -p case.mycase manifest.json`으로 파일에 든 것을 확인합니다.

## 벤더 분석기

엔진은 공개된 지식으로 고장을 분류합니다. OCP FMFM 프레임워크의 Level 1 고장 모드입니다. DRAM 제조사만 아는 것도 있습니다. 디바이스 안에서 어느 row가 서로 이웃인지, 부품번호별로 어느 정도의 오류율이 보통인지, 고장 난 DQ의 패턴이 무엇을 뜻하는지입니다. 이런 지식은 벤더 분석기가 제공합니다. 벤더 분석기는 별도 프로그램이고, 언어와 라이선스에 제한이 없습니다.

```
my dimm CPU0_C1 --plugin /opt/vendor/analyzer
my report case.mycase -o fa.html --audience fa --plugin /opt/vendor/analyzer
```

프로그램은 DIMM별 오류 기록을 FMFM 필드 이름으로 표준 입력에서 받고, 분석 결과를 표준 출력에 씁니다. 상태를 저장하지 않습니다. 이웃 row를 알려 준 답은 고장 분류에 쓰이고, 백분위는 소견의 기준 항목에 들어갑니다. Level 2 출력은 불량 분석용 리포트에만 들어갑니다. 계약 내용은 [docs/plugin-contract.md](docs/plugin-contract.md)에 있습니다. [examples/plugin](examples/plugin)에 작은 분석기와 요청, 응답 예제가 있습니다.

## 모니터링

`my serve --metrics`는 같은 스캔을 주기적으로 실행해 DIMM별 소견과 오류 건수를 Prometheus에 제공합니다. DIMM 하나의 소견은 `/api/dimm/<슬롯|시리얼>`에서 JSON으로 받습니다. `--fleet`을 쓰면 exporter 하나가 플릿의 rasdaemon이 기록하는 저장소를 읽습니다. 주소, row, column을 레이블로 쓰는 메트릭은 없습니다. [contrib/](contrib/README.md)에 systemd 유닛, 소견이 나빠지면 울리는 알림 규칙, Alertmanager 라우트, Grafana 대시보드가 있습니다.

## 현재 상태

개발 초기이고 태그한 버전이 없습니다.

- 합성 호스트와 픽스처로 시험했습니다. 고장 모드, 규칙, 입력마다 시험이 있습니다.
- 실제 호스트 5대에서 일반 사용자와 root로 실행했습니다. x86_64와 aarch64이고 5대 모두 가상 머신입니다. 결과는 [docs/validation.md](docs/validation.md)에 있습니다.
- 랩 호스트에 page offline, machine check, 펌웨어 오류 기록, CXL 이벤트 기록을 주입해 캡처하고 주입 계획과 대조했습니다. 결과는 [docs/validation-lab.md](docs/validation-lab.md)에 있습니다.
- 소견을 공개 플릿 데이터셋의 장애 티켓과 대조했습니다. 결과는 [docs/evaluation.md](docs/evaluation.md)에 있습니다.
- 실제 메모리 컨트롤러의 EDAC 드라이버가 올라간 호스트에서는 아직 검증하지 못했습니다. 그런 호스트의 기록 디코딩은 커널과 rasdaemon 소스를 따라 만든 픽스처로만 시험했습니다.
- 메모리 벤더의 분석기를 플러그인 계약에 연결해 실행해 본 적이 없습니다.
- 실제 서버의 BMC는 읽어 보지 못했습니다. Redfish 클라이언트는 테스트의 대역 BMC와 DMTF 목업 서버에서만 실행했습니다([docs/redfish.md](docs/redfish.md#lab-check)).

## 로드맵

- 베어메탈에서 Intel, AMD, ARM 서버의 EDAC 드라이버로 오류를 주입하고 `my verify`로 대조합니다.
- linux/amd64, linux/arm64, darwin/arm64 아카이브가 든 첫 태그 릴리스를 냅니다([docs/release.md](docs/release.md)).
- 임계값을 불량 분석 결과와 대조합니다.
- DDR5의 ECS 값과 DIMM의 리페어 예산을 그림으로 그립니다.
- 메모리 벤더의 분석기를 플러그인 계약에 연결해 실행합니다.

## 문서

`docs/` 아래 문서는 영어입니다.

| 문서 | 내용 |
| --- | --- |
| [docs/reading-guide.md](docs/reading-guide.md) | 그림마다 읽는 법 |
| [docs/rules.md](docs/rules.md) | 규칙별 조건, 임계값, 소견의 내용 |
| [docs/compatibility.md](docs/compatibility.md) | 입력별로 읽는 것, 관측 등급, 빠지는 것 |
| [docs/redfish.md](docs/redfish.md) | BMC 읽기: 보내는 요청, 인증서와 암호, BIOS 설정 대조, 운영체제와 BMC 비교, DIMM 라벨 |
| [docs/data-model.md](docs/data-model.md) | 원장: 테이블, 식별자, 여러 소스의 기록이 하나의 이력이 되는 과정 |
| [docs/bundle-format.md](docs/bundle-format.md) | 케이스 번들: 구성 파일, 매니페스트, 읽을 때의 검사, 익명화 |
| [docs/diff.md](docs/diff.md) | 한 호스트의 두 케이스 비교 |
| [docs/plugin-contract.md](docs/plugin-contract.md) | 벤더 분석기 계약 |
| [docs/fmfm-mapping.md](docs/fmfm-mapping.md) | 기록과 소견을 FMFM 필드 이름에 대응시킨 표 |
| [docs/exporter.md](docs/exporter.md) | 메트릭과 HTTP API |
| [docs/crash-loop.md](docs/crash-loop.md) | 치명적 메모리 오류 뒤 reset이 잃는 것과 그것을 남기는 설정 |
| [docs/verify.md](docs/verify.md) | 주입 계획과 기록의 대조 |
| [docs/evaluation.md](docs/evaluation.md) | 필드 데이터셋과 대조한 결과 |
| [docs/validation.md](docs/validation.md), [docs/validation-lab.md](docs/validation-lab.md) | 실제 호스트 실행 결과와 랩의 오류 주입 |
| [docs/release.md](docs/release.md) | 릴리스를 빌드하고 검증하는 방법 |

## 기여

빌드, 시험, 변경을 보내는 방법과 소스 트리 구조는 [CONTRIBUTING.md](CONTRIBUTING.md)에 있습니다. 보안 문제는 [SECURITY.md](SECURITY.md)에 적힌 경로로 알려 주세요.

## 라이선스

Apache-2.0입니다. [LICENSE](LICENSE)를 보세요.
