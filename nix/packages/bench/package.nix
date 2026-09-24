{ writeShellApplication
, python3
, git
, jdk21
, gradle
, jman
, jmanSrc ? ../../..
}:

writeShellApplication {
  name = "jman-bench";
  runtimeInputs = [ python3 git jdk21 gradle jman ];
  text = ''exec python3 ${jmanSrc}/bench/jman_bench.py "$@"'';
}
