# Output of nvidia-smi

`nvidia-smi-q.txt`, `nvidia-smi-q.xml` and `nvidia-smi-q-legacy.txt` are written by hand in the layout of `nvidia-smi -q` and `-q -x`.

The CSV files are public samples of the selective queries, kept as they were published.

| File | Origin |
| --- | --- |
| `query-gpu-h200.csv` | `nvidia-smi --query-gpu=<all 241 properties> --format=csv` on an H200, driver 610.43.02. From the capture `linux-x86_64__nvidia-h200__610.43.02.txt` of github.com/utkuozdemir/nvidia_gpu_exporter (MIT license). Serial number and UUID were masked by its author. |
| `query-gpu-h200-mig.csv` | the same query on an H200 in MIG mode, where the driver reports the bank histogram and no row counts. Same repository. |
| `query-gpu-rtx2080-470.csv` | the same query (115 properties) on a GeForce RTX 2080 SUPER, driver 470.256.02: a GPU without ECC and without a serial number, and a driver that has no row remapping properties. Same repository. |
| `query-retired-pages-h200.csv` | `nvidia-smi --query-retired-pages=... --format=csv` of the H200 capture: one of the other selective queries, which the reader names and does not read. |
| `query-remapped-rows.csv` | `nvidia-smi --query-remapped-rows=... --format=csv` on a node with H200 GPUs, with its command line, as shown in the GPU debugging notes of github.com/stas00/ml-engineering. The line `[...]` stands for the rows its author left out. The query names no UUID and no serial number. |

The captures of the first repository also hold the text of `nvidia-smi --help-query-gpu` for each driver, which is where the property names in `internal/adapter/nvidia/csv.go` come from.
