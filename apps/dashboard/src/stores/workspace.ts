import { create } from "zustand";
import { persist } from "zustand/middleware";

type WorkspaceState = {
  apiKey: string;
  orgId: string;
  projectId: string;
  environmentId: string;
  connectionOpen: boolean;
  setApiKey: (apiKey: string) => void;
  setOrgId: (orgId: string) => void;
  setProjectId: (projectId: string) => void;
  setEnvironmentId: (environmentId: string) => void;
  openConnection: () => void;
  closeConnection: () => void;
  signOut: () => void;
};

export const useWorkspace = create<WorkspaceState>()(
  persist(
    (set) => ({
      apiKey: "",
      orgId: "",
      projectId: "",
      environmentId: "",
      connectionOpen: false,
      setApiKey: (apiKey) => set({ apiKey }),
      setOrgId: (orgId) => set({ orgId, projectId: "", environmentId: "" }),
      setProjectId: (projectId) => set({ projectId, environmentId: "" }),
      setEnvironmentId: (environmentId) => set({ environmentId }),
      openConnection: () => set({ connectionOpen: true }),
      closeConnection: () => set({ connectionOpen: false }),
      signOut: () =>
        set({
          apiKey: "",
          orgId: "",
          projectId: "",
          environmentId: "",
          connectionOpen: true,
        }),
    }),
    {
      name: "agent-platform-workspace",
      partialize: (state) => ({
        apiKey: state.apiKey,
        orgId: state.orgId,
        projectId: state.projectId,
        environmentId: state.environmentId,
      }),
    },
  ),
);
