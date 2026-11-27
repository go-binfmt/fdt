/* libfdt side of the benchmark: the same work as the Go side, on the same file.
 *
 *   libfdt_bench parse FILE   fdt_check_full, then visit every node and every
 *                             property (name and value pointer and length)
 *   libfdt_bench write FILE   the tree of FILE is collected once into a flat
 *                             list of operations (untimed), then written with
 *                             the sequential-write API (fdt_create ...
 *                             fdt_finish); only the writing is timed
 *
 * It prints the median ns per operation of 9 samples of at least 20 ms each,
 * and a checksum so the compiler cannot drop the work. */
#define _POSIX_C_SOURCE 200809L
#include <libfdt.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static double now(void)
{
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return ts.tv_sec * 1e9 + ts.tv_nsec;
}

static uint64_t visit(const void *fdt)
{
	uint64_t sum = 0;
	int node = 0, depth = 0;
	for (;;) {
		int prop;
		fdt_for_each_property_offset(prop, fdt, node) {
			int len;
			const char *name;
			const void *v = fdt_getprop_by_offset(fdt, prop, &name, &len);
			sum += (uint64_t)len + (uint8_t)name[0] + (v ? 1 : 0);
		}
		int next = fdt_next_node(fdt, node, &depth);
		if (next < 0)
			break;
		int l;
		sum += (uint8_t)fdt_get_name(fdt, next, &l)[0];
		node = next;
	}
	return sum;
}

enum { OP_BEGIN, OP_PROP, OP_END };
struct op {
	int kind;
	const char *name;
	const void *val;
	int len;
};

static struct op *collect(const void *fdt, size_t *nops)
{
	size_t cap = 1024, n = 0;
	struct op *ops = malloc(cap * sizeof *ops);
	int node = 0, depth = 0;
#define PUSH(k, nm, v, l) do { \
	if (n == cap) { cap *= 2; ops = realloc(ops, cap * sizeof *ops); } \
	ops[n++] = (struct op){ k, nm, v, l }; } while (0)
	PUSH(OP_BEGIN, "", NULL, 0);
	for (;;) {
		int prop;
		fdt_for_each_property_offset(prop, fdt, node) {
			int len;
			const char *name;
			const void *v = fdt_getprop_by_offset(fdt, prop, &name, &len);
			PUSH(OP_PROP, name, v, len);
		}
		int before = depth;
		int next = fdt_next_node(fdt, node, &depth);
		if (next < 0) {
			for (int d = before; d >= 0; d--)
				PUSH(OP_END, NULL, NULL, 0);
			break;
		}
		/* depth went from before to depth: close (before - depth + 1) nodes
		 * when it did not descend, none extra when it did. */
		for (int d = before; d >= depth; d--)
			PUSH(OP_END, NULL, NULL, 0);
		int l;
		PUSH(OP_BEGIN, fdt_get_name(fdt, next, &l), NULL, 0);
		node = next;
	}
	*nops = n;
	return ops;
}

static int cmp(const void *a, const void *b)
{
	double x = *(const double *)a, y = *(const double *)b;
	return (x > y) - (x < y);
}

int main(int argc, char **argv)
{
	if (argc != 3) {
		fprintf(stderr, "usage: %s parse|write FILE\n", argv[0]);
		return 2;
	}
	FILE *f = fopen(argv[2], "rb");
	if (!f) { perror(argv[2]); return 1; }
	fseek(f, 0, SEEK_END);
	long size = ftell(f);
	fseek(f, 0, SEEK_SET);
	char *buf = malloc(size);
	if (fread(buf, 1, size, f) != (size_t)size) return 1;
	fclose(f);

	int parse = strcmp(argv[1], "parse") == 0;
	if (!parse && strcmp(argv[1], "write") != 0) return 2;

	struct op *ops = NULL;
	size_t nops = 0;
	char *out = NULL;
	if (!parse) {
		ops = collect(buf, &nops);
		out = malloc((size_t)size * 2 + 4096);
	}

	uint64_t sink = 0;
	double samples[9];
	for (int s = 0; s < 9; s++) {
		long iters = 0;
		double t0 = now(), t1;
		do {
			if (parse) {
				if (fdt_check_full(buf, size) != 0) return 1;
				sink += visit(buf);
			} else {
				if (fdt_create(out, size * 2 + 4096) != 0) return 1;
				fdt_finish_reservemap(out);
				for (size_t i = 0; i < nops; i++) {
					switch (ops[i].kind) {
					case OP_BEGIN: fdt_begin_node(out, ops[i].name); break;
					case OP_PROP: fdt_property(out, ops[i].name, ops[i].val, ops[i].len); break;
					default: fdt_end_node(out);
					}
				}
				if (fdt_finish(out) != 0) return 1;
				sink += fdt_totalsize(out);
			}
			iters++;
			t1 = now();
		} while (t1 - t0 < 20e6);
		samples[s] = (t1 - t0) / iters;
	}
	qsort(samples, 9, sizeof samples[0], cmp);
	printf("%.0f %llu\n", samples[4], (unsigned long long)sink);
	return 0;
}
