import { createContext, useContext } from "react";

/** ShellContext lets a page open the navigation drawer on small screens. */
export const ShellContext = createContext<{ openNav: () => void }>({ openNav: () => {} });

export const useShell = () => useContext(ShellContext);
