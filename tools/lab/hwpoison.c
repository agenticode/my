// hwpoison: take pages of this process out of use the way the kernel does
// after a memory error, and say which pages they were.
//
//	hwpoison [-d DIR] [-t] KIND[:COUNT]...
//
//	anon     an anonymous page that was written       madvise(MADV_HWPOISON)
//	mlocked  the same, locked into memory             madvise(MADV_HWPOISON)
//	file     a page of a file, read and not written   madvise(MADV_HWPOISON)
//	soft     an anonymous page that was written       madvise(MADV_SOFT_OFFLINE)
//	hold     an anonymous page that was written       nothing: see below
//
// MADV_HWPOISON makes the kernel treat the page as hit by an uncorrected
// error: the page is unmapped and poisoned, and this process gets SIGBUS.
// MADV_SOFT_OFFLINE moves the content elsewhere and retires the page, as the
// kernel does for a page with too many corrected errors. Both need root and
// a kernel built with CONFIG_MEMORY_FAILURE.
//
// hold does nothing to its pages. It prints them, waits for a line on standard
// input and prints them again, so that another program can act on a page that
// is in use and see where its content went (the corrected error collector of
// the kernel takes page frame numbers through debugfs).
//
// -t reads every poisoned page once more afterwards, which is what a program
// does that still uses the memory.
// -d names the directory for the file whose pages are used (default ".").
//
// One line of JSON is printed per page: the kind, the page frame number, the
// virtual address, the time and the signal the kernel sent.
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <setjmp.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>

#ifndef MADV_HWPOISON
#define MADV_HWPOISON 100
#endif
#ifndef MADV_SOFT_OFFLINE
#define MADV_SOFT_OFFLINE 101
#endif
#ifndef BUS_MCEERR_AR
#define BUS_MCEERR_AR 4
#define BUS_MCEERR_AO 5
#endif

static sigjmp_buf back;
static volatile sig_atomic_t got_signal, got_code, got_lsb;
static void *volatile got_addr;

static void on_sigbus(int sig, siginfo_t *si, void *ctx)
{
	(void)ctx;
	got_signal = sig;
	got_code = si->si_code;
	got_addr = si->si_addr;
	got_lsb = si->si_addr_lsb;
	siglongjmp(back, 1);
}

static const char *code_name(int code)
{
	switch (code) {
	case BUS_MCEERR_AR:
		return "BUS_MCEERR_AR";
	case BUS_MCEERR_AO:
		return "BUS_MCEERR_AO";
	case BUS_ADRERR:
		return "BUS_ADRERR";
	}
	return "other";
}

// frame returns the page frame number behind a mapped virtual address.
static uint64_t frame(void *p, long page)
{
	uint64_t e = 0;
	int fd = open("/proc/self/pagemap", O_RDONLY);
	if (fd < 0)
		return 0;
	if (pread(fd, &e, sizeof e, ((uintptr_t)p / page) * sizeof e) != sizeof e)
		e = 0;
	close(fd);
	if (!(e & (1ULL << 63))) // not present
		return 0;
	return e & ((1ULL << 55) - 1);
}

static void stamp(char *buf, size_t n)
{
	struct timespec ts;
	struct tm tm;
	clock_gettime(CLOCK_REALTIME, &ts);
	gmtime_r(&ts.tv_sec, &tm);
	size_t k = strftime(buf, n, "%Y-%m-%dT%H:%M:%S", &tm);
	snprintf(buf + k, n - k, ".%03ldZ", ts.tv_nsec / 1000000);
}

static int touch;
static int seq;

// one takes one page offline and prints what happened.
static int one(const char *kind, const char *dir, long page)
{
	int advice = MADV_HWPOISON;
	char *p = MAP_FAILED;
	int fd = -1;
	char path[4096] = "";

	if (!strcmp(kind, "file")) {
		snprintf(path, sizeof path, "%s/hwpoison-page-XXXXXX", dir);
		fd = mkstemp(path);
		if (fd < 0) {
			fprintf(stderr, "hwpoison: %s: %s\n", path, strerror(errno));
			return 1;
		}
		char *zero = calloc(1, page);
		memset(zero, 0x5a, page);
		if (write(fd, zero, page) != page || fsync(fd) != 0) {
			fprintf(stderr, "hwpoison: write %s: %s\n", path, strerror(errno));
			return 1;
		}
		free(zero);
		p = mmap(NULL, page, PROT_READ, MAP_PRIVATE, fd, 0);
	} else {
		p = mmap(NULL, page, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	}
	if (p == MAP_FAILED) {
		fprintf(stderr, "hwpoison: mmap: %s\n", strerror(errno));
		return 1;
	}
	if (!strcmp(kind, "file")) {
		volatile char c = p[0]; // bring the page of the file in
		(void)c;
	} else {
		memset(p, 0xa5, page);
		if (!strcmp(kind, "mlocked") && mlock(p, page) != 0)
			fprintf(stderr, "hwpoison: mlock: %s\n", strerror(errno));
		if (!strcmp(kind, "soft"))
			advice = MADV_SOFT_OFFLINE;
	}

	uint64_t pfn = frame(p, page);
	char when[64];
	stamp(when, sizeof when);
	volatile int rc = 0, err = 0;
	got_signal = 0;
	if (sigsetjmp(back, 1) == 0) {
		rc = madvise(p, page, advice);
		err = errno;
	}
	int sig = got_signal, code = got_code, lsb = got_lsb;

	int touched_sig = 0, touched_code = 0;
	if (touch && advice == MADV_HWPOISON) {
		got_signal = 0;
		if (sigsetjmp(back, 1) == 0) {
			volatile char c = p[0];
			(void)c;
		}
		touched_sig = got_signal;
		touched_code = got_code;
	}
	// For a soft offline the content moved: the page behind the address is another one now.
	uint64_t after = advice == MADV_SOFT_OFFLINE ? frame(p, page) : 0;

	printf("{\"id\":\"%s-%d\",\"kind\":\"%s\",\"advice\":\"%s\",\"pfn\":\"0x%llx\",\"address\":\"0x%llx\",\"vaddr\":\"0x%llx\",\"time\":\"%s\"",
	       kind, ++seq, kind, advice == MADV_HWPOISON ? "MADV_HWPOISON" : "MADV_SOFT_OFFLINE",
	       (unsigned long long)pfn, (unsigned long long)pfn * (unsigned long long)page,
	       (unsigned long long)(uintptr_t)p, when);
	if (rc != 0)
		printf(",\"error\":\"%s\"", strerror(err));
	if (sig)
		printf(",\"signal\":\"SIGBUS\",\"si_code\":\"%s\",\"si_addr_lsb\":%d", code_name(code), lsb);
	if (touch && advice == MADV_HWPOISON) {
		if (touched_sig)
			printf(",\"read_again\":\"SIGBUS %s\"", code_name(touched_code));
		else
			printf(",\"read_again\":\"no signal\"");
	}
	if (after)
		printf(",\"pfn_after\":\"0x%llx\"", (unsigned long long)after);
	printf("}\n");
	fflush(stdout);

	munmap(p, page);
	if (fd >= 0) {
		close(fd);
		unlink(path);
	}
	return 0;
}

// hold maps count pages, prints them, waits for a line on standard input and
// prints where the content of each page is then.
static int hold(int count, long page)
{
	char **pages = calloc(count, sizeof *pages);
	uint64_t *pfns = calloc(count, sizeof *pfns);
	char when[64];
	for (int i = 0; i < count; i++) {
		pages[i] = mmap(NULL, page, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
		if (pages[i] == MAP_FAILED) {
			fprintf(stderr, "hwpoison: mmap: %s\n", strerror(errno));
			return 1;
		}
		memset(pages[i], 0xa5, page);
		pfns[i] = frame(pages[i], page);
		stamp(when, sizeof when);
		printf("{\"id\":\"hold-%d\",\"kind\":\"hold\",\"pfn\":\"0x%llx\",\"address\":\"0x%llx\",\"vaddr\":\"0x%llx\",\"time\":\"%s\"}\n",
		       i + 1, (unsigned long long)pfns[i], (unsigned long long)pfns[i] * (unsigned long long)page,
		       (unsigned long long)(uintptr_t)pages[i], when);
	}
	fflush(stdout);
	char line[64];
	if (!fgets(line, sizeof line, stdin))
		line[0] = 0;
	for (volatile int i = 0; i < count; i++) {
		// A page the kernel poisoned in the meantime answers a read with SIGBUS.
		volatile int value = -1;
		got_signal = 0;
		if (sigsetjmp(back, 1) == 0)
			value = (unsigned char)pages[i][0];
		const char *content = got_signal ? "lost" : value == 0xa5 ? "intact" : "changed";
		stamp(when, sizeof when);
		printf("{\"id\":\"hold-%d\",\"kind\":\"held\",\"pfn\":\"0x%llx\",\"pfn_after\":\"0x%llx\",\"content\":\"%s\"",
		       i + 1, (unsigned long long)pfns[i], (unsigned long long)(got_signal ? 0 : frame(pages[i], page)), content);
		if (got_signal)
			printf(",\"signal\":\"SIGBUS\",\"si_code\":\"%s\"", code_name(got_code));
		printf(",\"time\":\"%s\"}\n", when);
	}
	fflush(stdout);
	return 0;
}

int main(int argc, char **argv)
{
	const char *dir = ".";
	long page = sysconf(_SC_PAGESIZE);
	int opt;
	while ((opt = getopt(argc, argv, "d:t")) != -1) {
		switch (opt) {
		case 'd':
			dir = optarg;
			break;
		case 't':
			touch = 1;
			break;
		default:
			fprintf(stderr, "usage: hwpoison [-d DIR] [-t] KIND[:COUNT]...  (anon, mlocked, file, soft, hold)\n");
			return 2;
		}
	}
	if (optind == argc) {
		fprintf(stderr, "usage: hwpoison [-d DIR] [-t] KIND[:COUNT]...  (anon, mlocked, file, soft, hold)\n");
		return 2;
	}
	struct sigaction sa;
	memset(&sa, 0, sizeof sa);
	sa.sa_sigaction = on_sigbus;
	sa.sa_flags = SA_SIGINFO;
	sigaction(SIGBUS, &sa, NULL);

	for (int i = optind; i < argc; i++) {
		char kind[32];
		int count = 1;
		const char *colon = strchr(argv[i], ':');
		size_t n = colon ? (size_t)(colon - argv[i]) : strlen(argv[i]);
		if (n >= sizeof kind)
			n = sizeof kind - 1;
		memcpy(kind, argv[i], n);
		kind[n] = 0;
		if (colon)
			count = atoi(colon + 1);
		if (!strcmp(kind, "hold")) {
			if (hold(count, page) != 0)
				return 1;
			continue;
		}
		if (strcmp(kind, "anon") && strcmp(kind, "mlocked") && strcmp(kind, "file") && strcmp(kind, "soft")) {
			fprintf(stderr, "hwpoison: unknown kind %s\n", kind);
			return 2;
		}
		for (int k = 0; k < count; k++) {
			if (one(kind, dir, page) != 0)
				return 1;
			// Spread the kernel's messages over more than one second, so
			// that records of one second can be told from each other.
			usleep(300 * 1000);
		}
	}
	return 0;
}
