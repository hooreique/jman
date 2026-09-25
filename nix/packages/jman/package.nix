{
  buildGoModule,
  makeWrapper,
  lib,
  jdk21,
  jdt-language-server,
  gradle,
  lombokAgent,
  extension,
  jmanSrc ? ../../..,
}:

buildGoModule {
  pname = "jman";
  version = "0.1.0";
  src = jmanSrc;
  vendorHash = null;

  nativeBuildInputs = [ makeWrapper ];

  checkPhase = ''
    runHook preCheck
    go test -race ./...
    go vet ./...
    runHook postCheck
  '';

  postInstall = ''
    mkdir -p "$out/share/jman"
    cp -r skills gradle "$out/share/jman/"
    wrapProgram "$out/bin/jman" \
      --set-default JMAN_JDTLS ${jdt-language-server}/bin/jdtls \
      --set-default JMAN_JDTLS_HOME ${jdt-language-server}/share/java/jdtls \
      --set-default JMAN_LOMBOK_AGENT ${lombokAgent} \
      --set-default JMAN_JAVA_HOME ${jdk21} \
      --set-default JMAN_EXTENSION ${extension}/share/java/jman-jdt.jar \
      --set-default JMAN_GRADLE_HOME ${gradle}/libexec/gradle \
      --set-default JMAN_GRADLE ${gradle}/bin/gradle \
      --set-default JMAN_GRADLE_MODEL "$out/share/jman/gradle/model.gradle" \
      --set-default JMAN_SKILL "$out/share/jman/skills/jman/SKILL.md"
  '';

  meta = {
    description = "Build-aware Java symbol navigation for AI agents";
    homepage = "https://github.com/hooreique/jman";
    license = lib.licenses.mit;
    mainProgram = "jman";
  };
}
