'use client';

import { useEffect, useState } from 'react';
import { Inter } from 'next/font/google';
import { usePathname } from 'next/navigation';
import Link from 'next/link';
import './globals.css';
import { AuthProvider, useAuth } from '../context/auth';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

const inter = Inter({ subsets: ['latin'] });

// Icons as simple SVG components
const DashboardIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2V6zM14 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2V6zM4 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2v-2zM14 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2v-2z" />
  </svg>
);

const RigsIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2zM9 9h6v6H9V9z" />
  </svg>
);

const WalletIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 10h18M7 15h1m4 0h1m-7 4h12a3 3 0 003-3V8a3 3 0 00-3-3H6a3 3 0 00-3 3v8a3 3 0 003 3z" />
  </svg>
);

const PoolIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" />
  </svg>
);

const FlightSheetIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
  </svg>
);

const AlertIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9" />
  </svg>
);

const OCIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
  </svg>
);

const GroupIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
  </svg>
);

const SettingsIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
  </svg>
);

const LogoutIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1" />
  </svg>
);

const UsersIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z" />
  </svg>
);

const MenuIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
  </svg>
);

const CloseIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
  </svg>
);

const navItems = [
  { href: '/', label: 'Dashboard', icon: DashboardIcon },
  { href: '/rigs', label: 'Rigs', icon: RigsIcon },
  { href: '/rig-groups', label: 'Rig Groups', icon: GroupIcon },
  { href: '/wallets', label: 'Wallets', icon: WalletIcon },
  { href: '/pools', label: 'Pools', icon: PoolIcon },
  { href: '/flight-sheets', label: 'Flight Sheets', icon: FlightSheetIcon },
  { href: '/oc-profiles', label: 'OC Profiles', icon: OCIcon },
  { href: '/alerts', label: 'Alerts', icon: AlertIcon },
  { href: '/users', label: 'Users', icon: UsersIcon, adminOnly: true },
  { href: '/settings', label: 'Settings', icon: SettingsIcon },
];

// Public routes that don't need sidebar
const publicRoutes = ['/login', '/setup'];

function AppContent({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const { user, logout, isLoading } = useAuth();
  const [alertCount, setAlertCount] = useState(0);
  const [sidebarOpen, setSidebarOpen] = useState(false);

  const isPublicRoute = publicRoutes.includes(pathname);

  // Close sidebar on route change (mobile)
  useEffect(() => {
    setSidebarOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (isPublicRoute) return;

    async function fetchAlertCount() {
      try {
        const res = await fetch(`${getApiUrl()}/api/alerts/count`, {
          credentials: 'include',
        });
        const data = await res.json();
        setAlertCount(data.count || 0);
      } catch (err) {
        console.error('Failed to fetch alert count');
      }
    }

    fetchAlertCount();
    const interval = setInterval(fetchAlertCount, 30000);
    return () => clearInterval(interval);
  }, [isPublicRoute]);

  const isActive = (href: string) => {
    if (href === '/') return pathname === '/';
    return pathname.startsWith(href);
  };

  // Public routes - render children directly without sidebar
  if (isPublicRoute) {
    return <>{children}</>;
  }

  // Show loading while auth is being checked
  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blox-500"></div>
      </div>
    );
  }

  // Sidebar content (reused for both mobile and desktop)
  const SidebarContent = () => (
    <>
      {/* Logo */}
      <div className="p-4 lg:p-5 border-b border-slate-700/50">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="w-9 h-9 lg:w-10 lg:h-10 rounded-xl bg-gradient-to-br from-blox-400 to-blox-600 flex items-center justify-center font-bold text-base lg:text-lg">
              B
            </div>
            <div>
              <h1 className="text-base lg:text-lg font-bold text-white">BloxOs</h1>
              <p className="text-xs text-slate-400 hidden sm:block">Mining Management</p>
            </div>
          </div>
          {/* Close button - mobile only */}
          <button
            onClick={() => setSidebarOpen(false)}
            className="lg:hidden p-2 -mr-2 text-slate-400 hover:text-white"
          >
            <CloseIcon />
          </button>
        </div>
      </div>

      {/* Navigation */}
      <nav className="flex-1 p-3 lg:p-4 overflow-y-auto">
        <ul className="space-y-1">
          {navItems.map((item) => {
            // Skip admin-only items for non-admins
            if ((item as { adminOnly?: boolean }).adminOnly && user?.role !== 'ADMIN') {
              return null;
            }
            const Icon = item.icon;
            const active = isActive(item.href);
            const showBadge = item.href === '/alerts' && alertCount > 0;
            return (
              <li key={item.href}>
                <Link
                  href={item.href}
                  onClick={() => setSidebarOpen(false)}
                  className={`flex items-center gap-3 px-3 py-2.5 rounded-lg transition-all duration-200 ${
                    active
                      ? 'bg-blox-600/20 text-blox-400 border border-blox-500/30'
                      : 'text-slate-400 hover:bg-slate-700/50 hover:text-white border border-transparent'
                  }`}
                >
                  <Icon />
                  <span className="font-medium flex-1 text-sm lg:text-base">{item.label}</span>
                  {showBadge && (
                    <span className="px-2 py-0.5 text-xs font-bold bg-red-500 text-white rounded-full min-w-[20px] text-center">
                      {alertCount > 99 ? '99+' : alertCount}
                    </span>
                  )}
                </Link>
              </li>
            );
          })}
        </ul>
      </nav>

      {/* User Footer */}
      <div className="p-3 lg:p-4 border-t border-slate-700/50">
        <div className="flex items-center gap-3 px-2 lg:px-3 py-2">
          <div className="w-8 h-8 rounded-full bg-gradient-to-br from-blox-400 to-blox-600 flex items-center justify-center text-sm font-bold uppercase flex-shrink-0">
            {user?.name?.[0] || user?.email?.[0] || 'U'}
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium truncate">{user?.name || 'User'}</p>
            <p className="text-xs text-slate-500 truncate">{user?.email}</p>
          </div>
          <button
            onClick={logout}
            className="p-2 text-slate-400 hover:text-red-400 hover:bg-slate-700/50 rounded-lg transition-colors flex-shrink-0"
            title="Logout"
          >
            <LogoutIcon />
          </button>
        </div>
      </div>
    </>
  );

  // Protected routes - render with sidebar
  return (
    <div className="min-h-screen">
      {/* Mobile Header */}
      <header className="lg:hidden fixed top-0 left-0 right-0 z-40 bg-slate-800/95 backdrop-blur-sm border-b border-slate-700/50">
        <div className="flex items-center justify-between px-4 py-3">
          <button
            onClick={() => setSidebarOpen(true)}
            className="p-2 -ml-2 text-slate-400 hover:text-white"
          >
            <MenuIcon />
          </button>
          <div className="flex items-center gap-2">
            <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-blox-400 to-blox-600 flex items-center justify-center font-bold text-sm">
              B
            </div>
            <span className="font-semibold">BloxOs</span>
          </div>
          <div className="w-10" /> {/* Spacer for centering */}
        </div>
      </header>

      {/* Mobile Sidebar Overlay */}
      {sidebarOpen && (
        <div
          className="lg:hidden fixed inset-0 z-50 bg-black/60 backdrop-blur-sm"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside
        className={`
          fixed top-0 left-0 z-50 h-full w-64 bg-slate-800/95 backdrop-blur-sm border-r border-slate-700/50 flex flex-col
          transform transition-transform duration-300 ease-in-out
          lg:transform-none lg:z-30
          ${sidebarOpen ? 'translate-x-0' : '-translate-x-full lg:translate-x-0'}
        `}
      >
        <SidebarContent />
      </aside>

      {/* Main content */}
      <main className="lg:ml-64 pt-16 lg:pt-0 min-h-screen">
        <div className="p-4 lg:p-6 max-w-7xl mx-auto">
          {children}
        </div>
      </main>
    </div>
  );
}

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" className="dark">
      <head>
        <title>BloxOs - Mining Rig Management</title>
        <meta name="description" content="Open-source mining rig management system" />
        <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1" />
      </head>
      <body className={`${inter.className} bg-slate-900 text-white`}>
        <AuthProvider>
          <AppContent>{children}</AppContent>
        </AuthProvider>
      </body>
    </html>
  );
}
