"use client";

import { useEffect } from "react";

import { Select } from "@/components/ui/select";
import { useEnvironments, useOrganizations, useProjects } from "@/lib/api/hooks";
import { useWorkspace } from "@/stores/workspace";

export function WorkspaceSelectors({ compact = false }: { compact?: boolean }) {
  const orgId = useWorkspace((state) => state.orgId);
  const projectId = useWorkspace((state) => state.projectId);
  const environmentId = useWorkspace((state) => state.environmentId);
  const setOrgId = useWorkspace((state) => state.setOrgId);
  const setProjectId = useWorkspace((state) => state.setProjectId);
  const setEnvironmentId = useWorkspace((state) => state.setEnvironmentId);
  const organizations = useOrganizations();
  const projects = useProjects(orgId);
  const environments = useEnvironments(orgId, projectId);

  useEffect(() => {
    const items = organizations.data?.items ?? [];
    const first = items[0];
    if (first && !items.some((item) => item.id === orgId)) setOrgId(first.id);
  }, [organizations.data, orgId, setOrgId]);

  useEffect(() => {
    const items = projects.data?.items ?? [];
    const first = items[0];
    if (!orgId || !first) return;
    if (!items.some((item) => item.id === projectId)) setProjectId(first.id);
  }, [projects.data, orgId, projectId, setProjectId]);

  useEffect(() => {
    const items = environments.data?.items ?? [];
    const first = items[0];
    if (!projectId || !first) return;
    if (!items.some((item) => item.id === environmentId)) setEnvironmentId(first.id);
  }, [environments.data, projectId, environmentId, setEnvironmentId]);

  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <Select
        aria-label="Organization"
        value={orgId}
        onChange={(event) => setOrgId(event.target.value)}
        className={compact ? "max-w-28" : "max-w-40"}
        disabled={organizations.isPending || (organizations.data?.items.length ?? 0) === 0}
      >
        {(organizations.data?.items ?? []).map((organization) => (
          <option key={organization.id} value={organization.id}>
            {organization.name}
          </option>
        ))}
        {organizations.data?.items.length === 0 ? <option>No organizations</option> : null}
      </Select>
      <Select
        aria-label="Project"
        value={projectId}
        onChange={(event) => setProjectId(event.target.value)}
        className={compact ? "max-w-28" : "max-w-40"}
        disabled={!orgId || projects.isPending || (projects.data?.items.length ?? 0) === 0}
      >
        {(projects.data?.items ?? []).map((project) => (
          <option key={project.id} value={project.id}>
            {project.name}
          </option>
        ))}
        {projects.data?.items.length === 0 ? <option>No projects</option> : null}
      </Select>
      <Select
        aria-label="Environment"
        value={environmentId}
        onChange={(event) => setEnvironmentId(event.target.value)}
        className={compact ? "hidden max-w-28 lg:block" : "max-w-36"}
        disabled={!projectId || (environments.data?.items.length ?? 0) === 0}
      >
        {(environments.data?.items ?? []).map((environment) => (
          <option key={environment.id} value={environment.id}>
            {environment.name}
          </option>
        ))}
        {environments.data?.items.length === 0 ? <option>No environment</option> : null}
      </Select>
    </div>
  );
}
