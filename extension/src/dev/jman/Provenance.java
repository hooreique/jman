package dev.jman;

import java.util.*;
import org.eclipse.core.resources.*;
import org.eclipse.core.runtime.*;
import org.eclipse.core.runtime.jobs.Job;
import org.eclipse.jdt.core.*;
import org.eclipse.jdt.core.search.*;
import org.eclipse.jdt.ls.core.internal.IDelegateCommandHandler;
import org.eclipse.jdt.ls.core.internal.JDTUtils;
import org.eclipse.jdt.ls.core.internal.JobHelpers;
import org.eclipse.jdt.ls.core.internal.ProjectUtils;
import org.eclipse.jdt.ls.core.internal.handlers.BaseDocumentLifeCycleHandler;

/** Extracts identity from JDT's selected element, never from a name search. */
public final class Provenance implements IDelegateCommandHandler {
    @Override
    public Object executeCommand(String command, List<Object> args, IProgressMonitor monitor) throws Exception {
        JobHelpers.waitForJobs(BaseDocumentLifeCycleHandler.DOCUMENT_LIFE_CYCLE_JOBS, monitor);
        if (command.equals("jman.sync")) {
            ResourcesPlugin.getWorkspace().getRoot().refreshLocal(IResource.DEPTH_INFINITE, monitor);
            JobHelpers.waitForWorkspaceJobsToComplete(monitor);
            Job.getJobManager().join(ResourcesPlugin.FAMILY_AUTO_BUILD, monitor);
            Job.getJobManager().join(ResourcesPlugin.FAMILY_MANUAL_BUILD, monitor);
            // A deliberately nonexistent package avoids materializing the entire type index.
            new SearchEngine().searchAllTypeNames("__jman_index_barrier__".toCharArray(), SearchPattern.R_EXACT_MATCH,
                null, SearchPattern.R_PATTERN_MATCH, IJavaSearchConstants.TYPE,
                SearchEngine.createWorkspaceScope(), new TypeNameRequestor() {},
                IJavaSearchConstants.WAIT_UNTIL_READY_TO_SEARCH, monitor);
            List<Map<String,Object>> problems = new ArrayList<>();
            for (IMarker marker : ResourcesPlugin.getWorkspace().getRoot().findMarkers(IMarker.PROBLEM, true, IResource.DEPTH_INFINITE)) {
                if (marker.getAttribute(IMarker.SEVERITY, 0) == IMarker.SEVERITY_ERROR && problems.size() < 100) {
                    problems.add(Map.of("message", marker.getAttribute(IMarker.MESSAGE, ""),
                        "path", marker.getResource().getFullPath().toString()));
                }
            }
            List<String> roots = new ArrayList<>();
            for (IProject project : ResourcesPlugin.getWorkspace().getRoot().getProjects()) {
                IPath path = ProjectUtils.getProjectRealFolder(project);
                if (path != null && !path.toString().contains("/.metadata/") && !project.getName().equals("jdt.ls-java-project")) roots.add(path.toOSString());
            }
            return Map.of("problems", problems, "projectRoots", roots);
        }
        String uri = (String) args.get(0);
        int line = ((Number) args.get(1)).intValue();
        int column = ((Number) args.get(2)).intValue();
        ICompilationUnit unit = JDTUtils.resolveCompilationUnit(uri);
        if (unit == null) return Map.of("resolved", false, "reason", "No imported compilation unit");
        String source = unit.getBuffer().getContents();
        int offset = 0;
        for (int n=0; n<line; n++) {
            int next = source.indexOf('\n', offset);
            if (next < 0) throw new IllegalArgumentException("Line outside source");
            offset = next + 1;
        }
        offset += column;
        if (offset > source.length()) throw new IllegalArgumentException("Column outside source");
        IJavaElement[] selected = unit.codeSelect(offset, 0);
        Map<String,Object> result = new LinkedHashMap<>();
        result.put("resolved", selected.length == 1);
        result.put("project", unit.getJavaProject().getElementName());
        result.put("sourceRoot", unit.getAncestor(IJavaElement.PACKAGE_FRAGMENT_ROOT).getPath().toString());
        if (selected.length != 1) { result.put("candidates", selected.length); return result; }
        IJavaElement element = selected[0];
        result.put("handle", element.getHandleIdentifier());
        result.put("name", signature(element));
        IJavaElement ancestor = element.getAncestor(IJavaElement.PACKAGE_FRAGMENT_ROOT);
        if (ancestor instanceof IPackageFragmentRoot root) {
            result.put("binary", root.isArchive() ? root.getPath().toOSString() : "");
            result.put("root", root.getPath().toString());
            IClasspathEntry entry = root.getRawClasspathEntry();
            if (entry != null) {
                result.put("classpathEntry", entry.getPath().toString());
                Map<String,String> attributes = new TreeMap<>();
                for (IClasspathAttribute attribute : entry.getExtraAttributes()) attributes.put(attribute.getName(), attribute.getValue());
                result.put("attributes", attributes);
            }
            IPath attachment = root.getSourceAttachmentPath();
            if (attachment != null) result.put("sourceAttachment", attachment.toOSString());
        }
        if (element instanceof IMember member) {
            result.put("member", true);
            result.put("binaryMember", member.isBinary());
            IType type = member instanceof IType t ? t : member.getDeclaringType();
            if (type != null) result.put("declaringType", type.getFullyQualifiedName());
            ISourceRange range = member.getSourceRange();
            result.put("hasSourceRange", range != null && range.getOffset() >= 0 && range.getLength() > 0);
            if (member.getClassFile() != null) result.put("attachedSourceAvailable", member.getClassFile().getSource() != null);
            if (!member.isBinary() && member instanceof IMethod && member.getCompilationUnit() != null) {
                ISourceRange nameRange = member.getNameRange();
                String ownerSource = member.getCompilationUnit().getBuffer().getContents();
                boolean namedInSource = nameRange != null && nameRange.getOffset() >= 0
                    && nameRange.getOffset() + nameRange.getLength() <= ownerSource.length()
                    && ownerSource.substring(nameRange.getOffset(), nameRange.getOffset() + nameRange.getLength()).equals(member.getElementName());
                result.put("generatedMember", !namedInSource);
            }
        }
        return result;
    }

    private static String signature(IJavaElement element) throws JavaModelException {
        if (element instanceof IMethod method) {
            String owner = method.getDeclaringType().getFullyQualifiedName();
            List<String> parameters = new ArrayList<>();
            for (String parameter : method.getParameterTypes()) parameters.add(Signature.toString(parameter));
            return owner + "." + method.getElementName() + "(" + String.join(", ", parameters) + "): " + Signature.toString(method.getReturnType());
        }
        if (element instanceof IType type) return type.getFullyQualifiedName();
        if (element instanceof IField field) return field.getDeclaringType().getFullyQualifiedName() + "." + field.getElementName() + ": " + Signature.toString(field.getTypeSignature());
        return element.getElementName();
    }
}
