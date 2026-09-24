{
  stdenvNoCC,
  jdk21,
  jdt-language-server,
  jmanSrc ? ../../..,
}:

stdenvNoCC.mkDerivation {
  pname = "jman-jdt-extension";
  version = "0.1.0";
  src = "${jmanSrc}/extension";

  nativeBuildInputs = [ jdk21 ];

  buildPhase = ''
    runHook preBuild
    mkdir classes
    javac -cp '${jdt-language-server}/share/java/jdtls/plugins/*' -d classes src/dev/jman/*.java
    jar cfm jman-jdt.jar META-INF/MANIFEST.MF -C classes . plugin.xml
    runHook postBuild
  '';

  installPhase = ''
    runHook preInstall
    install -Dm444 jman-jdt.jar "$out/share/java/jman-jdt.jar"
    runHook postInstall
  '';
}
