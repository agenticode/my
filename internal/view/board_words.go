package view

import "github.com/agenticode/my/internal/i18n"

// BoardMessages are the words of the board.
var BoardMessages = i18n.Catalog{
	"board.counts": {EN: "{placed} {placed|slot|slots} on the board, {nochannel} drawn without channels, {unplaced} set aside",
		KO: "보드에 그린 슬롯 {placed}개, 채널 없이 그린 슬롯 {nochannel}개, 따로 둔 슬롯 {unplaced}개"},
	"board.why.none": {EN: "{n} {n|slot|slots} whose names give no socket and no channel, such as {locator} / {bank}",
		KO: "이름에서 소켓과 채널이 읽히지 않는 슬롯 {n}개 (예: {locator} / {bank})"},
	"board.why.range": {EN: "{n} {n|slot|slots} with a socket or channel number too large, such as {locator} / {bank}",
		KO: "소켓이나 채널 번호가 너무 큰 슬롯 {n}개 (예: {locator} / {bank})"},
	"board.why.negative": {EN: "{n} {n|slot|slots} whose socket numbers start from a base that does not fit, such as {locator} / {bank}",
		KO: "소켓 번호의 시작 값이 맞지 않는 슬롯 {n}개 (예: {locator} / {bank})"},
	"board.why.assumed": {EN: "{n} {n|slot|slots} whose names give no socket on a board of several, such as {locator} / {bank}",
		KO: "소켓이 여럿인 보드에서 이름에 소켓이 없는 슬롯 {n}개 (예: {locator} / {bank})"},
	"board.why.run": {EN: "{n} {n|slot|slots} on socket numbers the board does not have, such as {locator} / {bank}",
		KO: "보드에 없는 소켓 번호가 붙은 슬롯 {n}개 (예: {locator} / {bank})"},
	"board.why.crowd": {EN: "{n} {n|slot|slots} set aside: more than three fell in one channel and the names give no socket, such as {locator} / {bank}",
		KO: "이름에 소켓이 없고 한 채널에 넷 이상 몰린 슬롯 {n}개 (예: {locator} / {bank})"},
	"board.why.negative-channel": {EN: "{n} {n|slot|slots} set aside: a channel number below 0 and no socket in the names, such as {locator} / {bank}",
		KO: "이름에 소켓이 없고 채널 번호가 0보다 작은 슬롯 {n}개 (예: {locator} / {bank})"},
	"board.nochannel.crowd": {EN: "{n} {n|slot|slots} drawn without channels: more than three fell in one channel, such as {locator} / {bank}",
		KO: "한 채널에 넷 이상 몰려 채널 없이 그린 슬롯 {n}개 (예: {locator} / {bank})"},
	"board.nochannel.negative-channel": {EN: "{n} {n|slot|slots} drawn without channels: a channel number below 0, such as {locator} / {bank}",
		KO: "채널 번호가 0보다 작아 채널 없이 그린 슬롯 {n}개 (예: {locator} / {bank})"},
	"board.why.fault": {EN: "{n} {n|slot|slots} shown by name only: the slot names could not be read",
		KO: "슬롯 이름을 읽지 못해 이름으로만 보인 슬롯 {n}개"},
	"board.why.count": {EN: "{n} {n|slot|slots} shown by name only: the inventory did not read back slot by slot",
		KO: "인벤토리가 슬롯마다 다시 읽히지 않아 이름으로만 보인 슬롯 {n}개"},
	"board.why.lost": {EN: "{n} {n|slot|slots} the arrangement did not hold, such as {locator} / {bank}",
		KO: "배치에 들어가지 않은 슬롯 {n}개 (예: {locator} / {bank})"},
	"board.caches.lshw":  {EN: "cache sizes from lshw (all cores of a package)", KO: "캐시 크기는 lshw에서 읽음 (패키지의 모든 코어)"},
	"board.caches.sysfs": {EN: "cache sizes from sysfs (one CPU of each package)", KO: "캐시 크기는 sysfs에서 읽음 (패키지마다 CPU 하나)"},
}
