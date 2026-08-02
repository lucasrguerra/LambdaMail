import { redirect } from "next/navigation";

/**
 * The root sends everyone straight into the mail client.
 *
 * This used to be a "choose your surface" splash listing the webmail and the
 * admin console side by side, which asked visitors a question they should never
 * have to answer: almost everyone wants their mail, and the handful who also
 * administer the server reach the console from inside the app once they are
 * signed in. It also advertised cookie names, audiences and internal paths to
 * anonymous visitors for no reason.
 *
 * Anyone without a session is redirected on to /user/login by the middleware,
 * so this needs no session check of its own.
 */
export default function RootPage() {
  redirect("/user/mail/inbox");
}
