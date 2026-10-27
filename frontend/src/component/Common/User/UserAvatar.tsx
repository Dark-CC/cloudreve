import { Avatar, Skeleton } from "@mui/material";
import { grey } from "@mui/material/colors";
import { bindHover, bindPopover } from "material-ui-popup-state";
import { usePopupState } from "material-ui-popup-state/hooks";
import { memo, useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useInView } from "react-intersection-observer";
import { ApiPrefix } from "../../../api/request.ts";
import { User } from "../../../api/user.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { loadUserInfo } from "../../../redux/thunks/session.ts";
import InPrivate from "../../Icons/InPrivate.tsx";
import UserPopover from "./UserPopover.tsx";

export interface UserAvatarProps {
  user?: User;
  uid?: string;
  overwriteTextSize?: boolean;
  onUserLoaded?: (user: User) => void;
  enablePopover?: boolean;
  square?: boolean;
  cacheKey?: string;
  [key: string]: any;
}

export const AnonymousUser: User = {
  id: "",
  nickname: "",
  created_at: "",
};

const UserAvatar = memo(
  ({ user, overwriteTextSize, onUserLoaded, uid, sx, square, cacheKey, enablePopover, ...rest }: UserAvatarProps) => {
    const [loadedUser, setLoadedUser] = useState<User | undefined>(undefined);
    const dispatch = useAppDispatch();
    const popupState = usePopupState({
      variant: "popover",
      popupId: "user",
    });
    const { t } = useTranslation();
    const { ref, inView } = useInView({
      triggerOnce: true,
      rootMargin: "200px 0px",
      skip: !!user,
    });
    useEffect(() => {
      if (inView && !loadedUser) {
        if (uid) {
          loadUser(uid);
        }
      }
    }, [inView]);
    useEffect(() => {
      if (user) {
        setLoadedUser(user);
      }
    }, [user]);
    const loadUser = useCallback(
      async (uid: string) => {
        try {
          const u = await dispatch(loadUserInfo(uid));
          setLoadedUser(u);
          if (onUserLoaded) {
            onUserLoaded(u);
          }
        } catch (e) {
          console.warn("Failed to load user info", e);
        }
      },
      [dispatch, setLoadedUser, onUserLoaded],
    );

    const avatarUrl = useMemo(() => {
      // Backend 404s the endpoint when no avatar is set; skip the request and
      // let Avatar fall back to initials.
      if (loadedUser && loadedUser.avatar) {
        return ApiPrefix + `/user/avatar/${loadedUser.id}${cacheKey ? `?nocache=1&key=${cacheKey ?? 0}` : ""}`;
      }
      return undefined;
    }, [loadedUser, cacheKey]);

    return (
      <>
        {loadedUser && (
          <>
            <Avatar
              alt={loadedUser.nickname ?? t("modals.anonymous")}
              src={avatarUrl}
              slotProps={{
                img: {
                  loading: "lazy",
                  alt: "",
                  draggable: false,
                },
              }}
              {...rest}
              {...bindHover(popupState)}
              sx={[
                {
                  // 默认头像统一为灰底白字（昵称首字母），不再按昵称哈希取彩色。
                  bgcolor: grey[500],
                  ...sx,
                },
                overwriteTextSize && {
                  fontSize: `${sx.width * 0.6}px!important`,
                },
                square && {
                  borderRadius: (theme) => `${theme.shape.borderRadius}px`,
                },
              ]}
            >
              {loadedUser.id == "" ? (
                <InPrivate
                  sx={{
                    ...(overwriteTextSize
                      ? {
                          fontSize: `${sx.width * 0.6}px`,
                        }
                      : {}),
                  }}
                />
              ) : undefined}
            </Avatar>
            {enablePopover && user && <UserPopover user={user} {...bindPopover(popupState)} />}
          </>
        )}
        {!loadedUser && <Skeleton ref={ref} variant={"circular"} sx={{ ...sx }} {...rest} />}
      </>
    );
  },
);

export default UserAvatar;
